package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

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

	if req.Token == "" || len(req.Token) < 50 || len(req.Token) > 300 {
		http.Error(w, "Invalid token ", http.StatusBadRequest)
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

	// Operator-level kill switch, independent of the owner's on_notification.
	if ch, err := dbGetChannel(ctx, slug); err == nil && !ch.Features.Notifications {
		return
	}

	channelCfg := getChannelConfig(ctx, slug)
	if !channelCfg.OnNotification {
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

	channelName, err := getChannelDetails(ctx, slug)
	if err != nil {
		log.Println("Failed to get channel details:", err)
		return
	}

	fcmSet := &FcmJsonConfing{
		Type:                    cfg.FcmJson.Type,
		ProjectId:               cfg.FcmJson.ProjectId,
		PrivateKeyId:            cfg.FcmJson.PrivateKeyId,
		PrivateKey:              cfg.FcmJson.PrivateKey,
		ClientEmail:             cfg.FcmJson.ClientEmail,
		ClientId:                cfg.FcmJson.ClientId,
		AuthUri:                 cfg.FcmJson.AuthUri,
		TokenUri:                cfg.FcmJson.TokenUri,
		AuthProviderX509CertUrl: cfg.FcmJson.AuthProviderX509CertUrl,
		ClientX509CertUrl:       cfg.FcmJson.ClientX509CertUrl,
		UniverseDomain:          cfg.FcmJson.UniverseDomain,
	}

	fcmSetJson, err := json.Marshal(fcmSet)
	if err != nil {
		log.Println("Failed to marshal FCM credentials:", err)
		return
	}

	client, err := getFcmClient(fcmSetJson)
	if err != nil {
		log.Println("Failed to create FCM client:", err)
		return
	}

	data := map[string]string{
		// project_domain is global, so it must be joined with the channel slug
		// or every channel's notification opens the same page.
		"url":   strings.TrimRight(cfg.ProjectDomain, "/") + "/channel/" + slug,
		"title": channelName["name"],
		"body":  m.Text,
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
	}
}
