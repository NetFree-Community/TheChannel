package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Transactional email for channel owners and support requesters is built and
// sent by Magnet (its own sender name, its own templates, replies land in its
// inbox). This side only tells Magnet that something happened, over one
// server-to-server endpoint shared with Magnet's implementation:
//
//	POST $MAGNET_MAIL_URL
//	x-channels-secret: $MAGNET_MAIL_SECRET
//	{"event": "channel_created" | "support_reply", "idempotency_key": ..., ...}
//
// Both variables unset (or either one) means the feature is off: nothing is
// sent and that is logged once. A mail is never on the request path — it is
// handed to a goroutine with its own deadline after the action it reports has
// been stored, so a slow or failing Magnet can neither delay nor fail opening a
// channel or answering a ticket.

const (
	magnetMailEventChannelCreated = "channel_created"
	magnetMailEventSupportReply   = "support_reply"

	// defaultPublicBaseURL is where every link in the mail points when the
	// operator has not configured a domain. Magnet refuses links to any other
	// host, so this is also the only value that works with it today.
	defaultPublicBaseURL = "https://arootz.magnetads.co.il"

	magnetMailAttempts    = 3
	maxIdempotencyKeyLen  = 200
	magnetMailOverallTime = 2 * time.Minute
)

var (
	magnetMailURL    = strings.TrimSpace(os.Getenv("MAGNET_MAIL_URL"))
	magnetMailSecret = strings.TrimSpace(os.Getenv("MAGNET_MAIL_SECRET"))
	publicBaseURLEnv = strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL"))

	// Per attempt. Magnet renders and hands the mail to its provider before it
	// answers, which is seconds, not milliseconds.
	magnetMailClient = &http.Client{Timeout: 15 * time.Second}

	// magnetMailBackoff is the pause before the 2nd and 3rd attempt. A var so
	// tests can shorten it.
	magnetMailBackoff = []time.Duration{3 * time.Second, 15 * time.Second}

	// magnetMailInFlight lets tests wait for background deliveries to settle.
	magnetMailInFlight sync.WaitGroup

	magnetMailOffLogged sync.Once
)

type magnetMailChannel struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	ManageURL string `json:"manage_url"`
}

type magnetMailTicket struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	ReplyText string `json:"reply_text"`
	URL       string `json:"url"`
}

type magnetMailEvent struct {
	Event          string             `json:"event"`
	IdempotencyKey string             `json:"idempotency_key"`
	To             string             `json:"to"`
	ToName         *string            `json:"to_name"`
	Channel        *magnetMailChannel `json:"channel"`
	ContactURL     string             `json:"contact_url"`
	Ticket         *magnetMailTicket  `json:"ticket,omitempty"`
}

// magnetMailEnabled reports whether both halves of the configuration are
// present, logging the "off" state once per process rather than per event.
func magnetMailEnabled() bool {
	if magnetMailURL != "" && magnetMailSecret != "" {
		return true
	}
	magnetMailOffLogged.Do(func() {
		log.Println("magnet mail: MAGNET_MAIL_URL or MAGNET_MAIL_SECRET is not set — channel emails are off")
	})
	return false
}

// publicBaseURL is the absolute origin every emailed link is built on. A mail
// has no request to derive a host from, so it takes the operator's configured
// project_domain (the value push notifications already use), then
// PUBLIC_BASE_URL, then the production address. Only https values count: a
// link in an email that lands on plain http, or on a bare host, is refused by
// Magnet and useless to the reader anyway.
func publicBaseURL() string {
	if cfg := getGlobalConfig(); cfg != nil {
		if d := strings.TrimRight(strings.TrimSpace(cfg.ProjectDomain), "/"); strings.HasPrefix(d, "https://") {
			return d
		}
	}
	if d := strings.TrimRight(publicBaseURLEnv, "/"); strings.HasPrefix(d, "https://") {
		return d
	}
	return defaultPublicBaseURL
}

// supportContactURL opens the support box on the landing page (?support=1,
// handled by LandingPageComponent), tagged with the channel when there is one
// so the operator sees which tenant is asking.
func supportContactURL(base, slug string) string {
	q := url.Values{"support": {"1"}}
	if slug != "" {
		q.Set("channel", slug)
	}
	return base + "/?" + q.Encode()
}

// supportTicketURL opens the support box with this thread expanded. It never
// carries the ticket's access token: the mail goes to an address the
// requester typed and nobody verified, and the token is the only credential on
// an anonymous thread — putting it in the link would hand the thread (and the
// right to answer as the requester) to whoever owns that address. A signed-in
// requester finds the thread after signing in; an anonymous one finds it in
// the browser that opened it. Everyone else has the reply text in the mail and
// can answer by replying to it.
func supportTicketURL(base, ticketID string) string {
	q := url.Values{"support": {"1"}, "ticket": {ticketID}}
	return base + "/?" + q.Encode()
}

func mailChannel(base string, ch *ChannelData) *magnetMailChannel {
	name := oneLine(ch.Name)
	if name == "" {
		name = ch.Slug
	}
	p := "/channel/" + url.PathEscape(ch.Slug)
	return &magnetMailChannel{
		Slug:      ch.Slug,
		Name:      name,
		URL:       base + p,
		ManageURL: base + p + "/manage",
	}
}

// mailToName is the greeting name, or null when all we have is the address
// itself (an anonymous ticket without a name stores the email as its name).
func mailToName(name, email string) *string {
	name = oneLine(name)
	if name == "" || strings.EqualFold(name, email) {
		return nil
	}
	return &name
}

// oneLine makes a free-text field safe for Magnet, which refuses control
// characters outside reply_text with a 400 (a lost mail, since a 400 is not
// retried) and caps names at 200 characters.
func oneLine(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)
	}), " ")
	return trimTo(s, 200)
}

func capKey(k string) string {
	if len(k) > maxIdempotencyKeyLen {
		return k[:maxIdempotencyKeyLen]
	}
	return k
}

// channelCreatedMail is the welcome mail to a channel's new owner. The slug
// is unique for the channel's whole life, so it alone keys the mail.
func channelCreatedMail(ch *ChannelData, ownerEmail, ownerName string) (*magnetMailEvent, bool) {
	if ch == nil || !looksLikeEmail(ownerEmail) {
		return nil, false
	}
	base := publicBaseURL()
	return &magnetMailEvent{
		Event:          magnetMailEventChannelCreated,
		IdempotencyKey: capKey("channel_created:" + ch.Slug),
		To:             ownerEmail,
		ToName:         mailToName(ownerName, ownerEmail),
		Channel:        mailChannel(base, ch),
		ContactURL:     supportContactURL(base, ch.Slug),
	}, true
}

// supportReplyMail tells the requester the management answered. replyIndex is
// the reply's position in the thread: messages are append-only and the thread
// is capped, so (ticket, index) names this one reply for good. ch is the
// channel the ticket was opened from, already filtered by ticketMailChannel,
// or nil.
func supportReplyMail(t *SupportTicket, replyIndex int, ch *ChannelData) (*magnetMailEvent, bool) {
	if t == nil || replyIndex < 0 || replyIndex >= len(t.Messages) {
		return nil, false
	}
	reply := t.Messages[replyIndex]
	if reply.Author != "admin" || !looksLikeEmail(t.Email) {
		return nil, false
	}
	base := publicBaseURL()
	ev := &magnetMailEvent{
		Event:          magnetMailEventSupportReply,
		IdempotencyKey: capKey(fmt.Sprintf("support_reply:%s:%d", t.ID, replyIndex)),
		To:             t.Email,
		ToName:         mailToName(t.Name, t.Email),
		ContactURL:     supportContactURL(base, ""),
		Ticket: &magnetMailTicket{
			ID:      t.ID,
			Subject: oneLine(t.Subject),
			// Exactly what the requester sees in the thread. The signature is
			// Magnet's template ("ניהול"); nothing here names who answered.
			ReplyText: reply.Body,
			URL:       supportTicketURL(base, t.ID),
		},
	}
	if ch != nil {
		ev.Channel = mailChannel(base, ch)
		ev.ContactURL = supportContactURL(base, ch.Slug)
	}
	return ev, true
}

// ticketMailChannel resolves the channel a ticket names, if its name and link
// may go into the requester's mail. ChannelSlug is free text from the request
// body, so anyone could name a private channel there; the mail must not tell
// them what it is called. A public channel is fine (its page says as much to
// anyone); a restricted one only for a signed-in requester who holds a role on
// it.
func ticketMailChannel(ctx context.Context, t *SupportTicket) *ChannelData {
	if t == nil || !isPlausibleSlug(t.ChannelSlug) {
		return nil
	}
	ch, err := dbGetChannel(ctx, t.ChannelSlug)
	if err != nil || ch == nil {
		return nil
	}
	if !ch.Features.Disabled && !ch.Features.RequireAuth {
		return ch
	}
	if !t.Authenticated {
		return nil
	}
	if v, ok := privilegesUsers.Load(normEmail(t.Email)); ok {
		if u, ok := v.(User); ok {
			if _, has := u.ChannelRoles[ch.Slug]; has {
				return ch
			}
		}
	}
	return nil
}

// dispatchMagnetMail builds and delivers one mail in the background. build
// runs inside the goroutine (it may read Redis) and returns false to send
// nothing. The caller has already stored the action; nothing here can affect
// its response.
func dispatchMagnetMail(build func(ctx context.Context) (*magnetMailEvent, bool)) {
	if !magnetMailEnabled() {
		return
	}
	magnetMailInFlight.Add(1)
	go func() {
		defer magnetMailInFlight.Done()
		defer func() {
			// A mail is never worth the process.
			if p := recover(); p != nil {
				log.Printf("magnet mail: panic: %v", p)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), magnetMailOverallTime)
		defer cancel()
		ev, ok := build(ctx)
		if !ok || ev == nil {
			return
		}
		if err := deliverMagnetMail(ctx, ev); err != nil {
			log.Printf("magnet mail: %s %s not delivered: %v", ev.Event, ev.IdempotencyKey, err)
		}
	}()
}

// errMagnetMailRejected marks a 4xx: Magnet read the request and refused it,
// so sending the same body again cannot succeed.
var errMagnetMailRejected = errors.New("rejected by magnet")

// deliverMagnetMail posts ev, retrying network errors and 5xx answers with a
// backoff. The idempotency key makes a retry after a lost 200 harmless.
// Neither the secret nor the recipient is ever written to the log.
func deliverMagnetMail(ctx context.Context, ev *magnetMailEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < magnetMailAttempts; attempt++ {
		if attempt > 0 {
			wait := magnetMailBackoff[min(attempt-1, len(magnetMailBackoff)-1)]
			select {
			case <-ctx.Done():
				return fmt.Errorf("gave up after %d attempts: %w (last: %v)", attempt, ctx.Err(), lastErr)
			case <-time.After(wait):
			}
		}
		status, err := postMagnetMail(ctx, payload)
		switch {
		case err != nil:
			lastErr = err
		case status >= 200 && status < 300:
			return nil
		case status == http.StatusConflict:
			// Magnet's "in_progress": the same key is mid-send in another
			// request. Not a refusal of the body, so it is worth another try.
			lastErr = fmt.Errorf("status %d", status)
		case status >= 400 && status < 500:
			return fmt.Errorf("%w: status %d", errMagnetMailRejected, status)
		default:
			lastErr = fmt.Errorf("status %d", status)
		}
	}
	return fmt.Errorf("gave up after %d attempts: %v", magnetMailAttempts, lastErr)
}

func postMagnetMail(ctx context.Context, payload []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, magnetMailURL, bytes.NewReader(payload))
	if err != nil {
		// The URL is operator configuration, not a secret, but the error text
		// is kept generic so a malformed value never echoes a pasted secret.
		return 0, errors.New("request build failed — check MAGNET_MAIL_URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-channels-secret", magnetMailSecret)

	resp, err := magnetMailClient.Do(req)
	if err != nil {
		// *url.Error names the URL; the cause alone is enough to act on.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return 0, uerr.Err
		}
		return 0, err
	}
	defer resp.Body.Close()
	// Drained (bounded) so the connection can be reused; the body is not
	// logged, since an error page might quote request headers back.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}
