package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"firebase.google.com/go/v4/messaging"
	"github.com/appleboy/go-fcm"
)

type FirebaseConfig struct {
	ApiKey            string `json:"apiKey"`
	AuthDomain        string `json:"authDomain"`
	ProjectId         string `json:"projectId"`
	StorageBucket     string `json:"storageBucket"`
	MessagingSenderId string `json:"messagingSenderId"`
	AppId             string `json:"appId"`
	MeasurementId     string `json:"measurementId"`
}

type FcmJsonConfing struct {
	Type                    string `json:"type"`
	ProjectId               string `json:"project_id"`
	PrivateKeyId            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientId                string `json:"client_id"`
	AuthUri                 string `json:"auth_uri"`
	TokenUri                string `json:"token_uri"`
	AuthProviderX509CertUrl string `json:"auth_provider_x509_cert_url"`
	ClientX509CertUrl       string `json:"client_x509_cert_url"`
	UniverseDomain          string `json:"universe_domain"`
}
type NotificationsConfig struct {
	EnableNotifications bool           `json:"enableNotifications"`
	VAPID               string         `json:"vapid"`
	FirebaseConfig      FirebaseConfig `json:"firebaseConfig"`
}

func getNotificationsConfig(w http.ResponseWriter, r *http.Request) {
	slug := channelSlugFromCtx(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	channelCfg := getChannelConfig(ctx, slug)
	cfg := getGlobalConfig()

	// Push needs all three: the platform configured (global), the tenant allowed
	// (operator toggle) and the owner opted in (channel setting).
	ch := channelFromCtx(r)
	featureAllowed := ch != nil && ch.Features.Notifications

	response := NotificationsConfig{
		EnableNotifications: cfg.OnNotification && channelCfg.OnNotification && featureAllowed,
		VAPID:               cfg.VAPID,
		FirebaseConfig: FirebaseConfig{
			ApiKey:            cfg.FcmApiKey,
			AuthDomain:        cfg.FcmAuthDomain,
			ProjectId:         cfg.FcmProjectId,
			StorageBucket:     cfg.FcmStorageBucket,
			MessagingSenderId: cfg.FcmMessagingSenderId,
			AppId:             cfg.FcmAppId,
			MeasurementId:     cfg.FcmMeasurementId,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

const firebaseMessagingSW = `
importScripts(
    "https://www.gstatic.com/firebasejs/11.4.0/firebase-app-compat.js"
);
importScripts(
    "https://www.gstatic.com/firebasejs/11.4.0/firebase-messaging-compat.js"
);

const firebaseConfig = {
    apiKey: "{{.FcmApiKey}}",
    authDomain: "{{.FcmAuthDomain}}",
    projectId: "{{.FcmProjectId}}",
    storageBucket: "{{.FcmStorageBucket}}",
    messagingSenderId: "{{.FcmMessagingSenderId}}",
    appId: "{{.FcmAppId}}",
    measurementId: "{{.FcmMeasurementId}}"
};

const app = firebase.initializeApp(firebaseConfig);
const messaging = firebase.messaging();

messaging.onBackgroundMessage((payload) => {
    self.registration.showNotification(payload.data?.title, {
        body: payload.data?.body,
        data: {
            url: payload.data?.url
        },
    });
});

self.addEventListener('notificationclick', (event) => {
    const url = event.notification.data.url;
    if (url) event.waitUntil(clients.openWindow(url));
});
`

func getFirebaseMessagingSW(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	tmpl, err := template.New("firebaseSW").Parse(firebaseMessagingSW)
	if err != nil {
		http.Error(w, "Failed to generate service worker", http.StatusInternalServerError)
		return
	}

	cfg := getGlobalConfig()
	err = tmpl.Execute(w, map[string]string{
		"FcmApiKey":            cfg.FcmApiKey,
		"FcmAuthDomain":        cfg.FcmAuthDomain,
		"FcmProjectId":         cfg.FcmProjectId,
		"FcmStorageBucket":     cfg.FcmStorageBucket,
		"FcmMessagingSenderId": cfg.FcmMessagingSenderId,
		"FcmAppId":             cfg.FcmAppId,
		"FcmMeasurementId":     cfg.FcmMeasurementId,
	})
	if err != nil {
		http.Error(w, "Failed to generate service worker", http.StatusInternalServerError)
		return
	}
}

// fcmTokenRe is the shape of an FCM registration token (base64url pieces
// joined by ':'). Anything else can never be delivered to, so accepting it
// only fills the subscription set with junk that costs an FCM slot per push.
var fcmTokenRe = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`)

// maxSubscriptionsPerChannel caps a channel's token set. Dead tokens are only
// pruned when a push fails for them, so a channel that never pushes keeps every
// token forever; the cap bounds what one tenant can cost the platform.
const maxSubscriptionsPerChannel = 50000

func subscribeNotifications(w http.ResponseWriter, r *http.Request) {
	slug := channelSlugFromCtx(r)

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.Token == "" || len(req.Token) < 50 || len(req.Token) > 300 || !fcmTokenRe.MatchString(req.Token) {
		http.Error(w, "Invalid token ", http.StatusBadRequest)
		return
	}

	// Every accepted token is a permanent entry in the channel's subscription
	// set, only pruned once FCM rejects it on a later push — so without a
	// limit one signed-in user could grow the set (and slow every post on
	// the channel) without bound. A device re-registers rarely.
	if !allowOrRetryAfter(w, subscribeLimiter(clientKey(r)), "too many subscription requests — please slow down") {
		return
	}

	// The token is stored even while push is off platform-wide. A short-circuit
	// that answered success without storing it told the user, on the bell,
	// that notifications were enabled while nothing was kept: a tab loaded
	// while push was on still shows the bell after the operator turns it off,
	// and once push came back the device was not in the set and nothing made
	// it retry. pushFcmMessage checks the global flag before it reads the set,
	// and the per-channel cap above bounds the growth.
	n, err := countSubscriptions(slug)
	if err != nil {
		http.Error(w, "Failed to subscribe to notifications", http.StatusInternalServerError)
		return
	}
	if n >= maxSubscriptionsPerChannel {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "too many subscriptions on this channel", http.StatusTooManyRequests)
		return
	}

	if err := addSubscription(slug, req.Token); err != nil {
		http.Error(w, "Failed to subscribe to notifications", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

const (
	maxPushTitleRunes = 100
	maxPushBodyRunes  = 300
)

var (
	// The composer's file markers, "[image-embedded#WxH](url)" and the
	// "[quote-embedded#]" prefix: neither means anything in a notification.
	pushEmbedMarker = regexp.MustCompile(`!?\[[a-z]+-embedded#[^\]]*\](\([^)]*\))?`)
	// A markdown link or image: keep the label, drop the URL.
	pushMarkdownLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	pushWhitespace   = regexp.MustCompile(`\s+`)
	pushEmphasis     = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")
)

// pushPreview reduces a post's markdown to the short plain text a notification
// can carry. Two reasons this is not just m.Text:
//   - the tray shows raw markup otherwise ("**", link URLs, embed markers);
//   - a data payload over 4 KiB is refused by FCM with INVALID_ARGUMENT for
//     every token in the batch — the same code a dead token gets — so one
//     long post not only failed to deliver but got every subscriber pruned.
func pushPreview(text string) string {
	t := pushEmbedMarker.ReplaceAllString(text, "")
	t = pushMarkdownLink.ReplaceAllString(t, "$1")
	t = pushEmphasis.Replace(t)
	t = strings.TrimSpace(pushWhitespace.ReplaceAllString(t, " "))
	return truncateRunes(t, maxPushBodyRunes)
}

// truncateRunes cuts on a character boundary, never inside a multi-byte rune.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max])) + "…"
}

// The FCM client owns an OAuth token source, so rebuilding one per push throws
// away every cached token and mints fresh credentials for every message. Cache
// it, keyed on the credential bytes so a settings change still takes effect.
var (
	fcmClientMu   sync.Mutex
	fcmClient     *fcm.Client
	fcmClientHash [32]byte
)

func getFcmClient(credentials []byte) (*fcm.Client, error) {
	h := sha256.Sum256(credentials)

	fcmClientMu.Lock()
	defer fcmClientMu.Unlock()

	if fcmClient != nil && h == fcmClientHash {
		return fcmClient, nil
	}

	// context.Background(), not the caller's: the cached client's token source
	// outlives the request that happened to create it.
	c, err := fcm.NewClient(context.Background(), fcm.WithCredentialsJSON(credentials))
	if err != nil {
		return nil, err
	}
	fcmClient, fcmClientHash = c, h
	return c, nil
}

func pushFcmMessage(slug string, m *Message) {
	// One snapshot for the whole function: reading the global per field would
	// let a concurrent settings save splice two service-account credentials.
	cfg := getGlobalConfig()
	if !cfg.OnNotification {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	channelCfg := getChannelConfig(ctx, slug)
	if !channelCfg.OnNotification {
		return
	}

	// Operator-level kill switch, independent of the owner's on_notification.
	// The channel read is reused below for the notification title, so the hash
	// is fetched exactly once per push.
	ch, chErr := dbGetChannel(ctx, slug)
	if chErr == nil && !ch.Features.Notifications {
		// The owner HAS push configured; going silent here without a trace makes
		// the kill switch undiagnosable.
		log.Printf("push suppressed for %s: notifications feature disabled by operator\n", slug)
		return
	}

	list, err := getSubcriptionsList(slug)
	if err != nil {
		log.Println("Failed to get subscription list:", err)
		return
	}

	if len(list) == 0 {
		log.Println("No subscriptions found, skipping FCM push")
		return
	}

	// cfg.FcmJson is already the complete credential snapshot (ToConfig always
	// allocates it); marshalling it directly means a field added to the struct
	// can never be silently dropped by a stale hand-written copy.
	fcmSetJson, err := json.Marshal(cfg.FcmJson)
	if err != nil {
		log.Println("Failed to marshal FCM credentials:", err)
		return
	}

	client, err := getFcmClient(fcmSetJson)
	if err != nil {
		log.Println("Failed to create FCM client:", err)
		return
	}

	// Preserve the empty-title behaviour when the channel read failed.
	title := ""
	if chErr == nil {
		title = ch.Name
	}
	data := map[string]string{
		// project_domain is global, so it must be joined with the channel slug
		// or every channel's notification opens the same page.
		"url":   strings.TrimRight(cfg.ProjectDomain, "/") + "/channel/" + slug,
		"title": truncateRunes(title, maxPushTitleRunes),
		"body":  pushPreview(m.Text),
	}

	// The send loop gets its own budget: ctx above also covers the config and
	// subscription lookups, and its deadline expiring mid-loop would silently
	// drop every remaining chunk.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer sendCancel()

	for chunk := range slices.Chunk(list, 500) {
		message := &messaging.MulticastMessage{
			Tokens: chunk,
			Data:   data,
		}

		r, err := client.SendMulticast(sendCtx, message)
		if err != nil {
			log.Println("Failed to send push notification:", err)
			continue
		}
		log.Printf("Push notification sent to %d tokens: \n", r.SuccessCount)
		log.Printf("Failed to send to %d tokens: \n", r.FailureCount)

		// Prune the tokens FCM reports as permanently dead, so the set does not
		// grow without bound and later pushes stop paying for them. Only
		// unregistered/invalid tokens are removed — a transient failure
		// (rate-limited, server error) is left in place to retry.
		if r.FailureCount > 0 {
			var dead, invalid []string
			for i, resp := range r.Responses {
				if resp.Success || i >= len(chunk) {
					continue
				}
				switch {
				case messaging.IsUnregistered(resp.Error):
					dead = append(dead, chunk[i])
				case messaging.IsInvalidArgument(resp.Error):
					invalid = append(invalid, chunk[i])
				}
			}
			// INVALID_ARGUMENT is also what FCM answers for a malformed
			// *message* (oversized payload, bad field) — and then every token
			// in the chunk fails with it at once. That is a bad send, not a
			// batch of dead tokens: pruning them would silently unsubscribe
			// the whole channel because of one long post.
			if len(invalid) == len(chunk) && len(chunk) > 1 {
				log.Printf("push to %s: every token rejected as INVALID_ARGUMENT, keeping subscriptions (payload problem?)\n", slug)
			} else {
				dead = append(dead, invalid...)
			}
			removeSubscriptions(slug, dead)
		}
	}
}
