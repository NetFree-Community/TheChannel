package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// webhookClient is shared across deliveries: a per-message http.Transport never
// expires its idle connections and is never collected, so sockets and their
// readLoop goroutines accumulate on busy channels. Certificate verification is
// left at the default (on) — the payload carries the channel's verify token.
var webhookClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
			// Control runs after DNS resolution with the address actually about
			// to be dialled, which is what makes this cover redirects and DNS
			// rebinding as well as the literal URL.
			Control: blockPrivateAddress,
		}).DialContext,
	},
}

// allowPrivateWebhookTargets re-enables webhooks to private addresses, for an
// operator running the whole platform inside a trusted network. Off by default:
// the URL is set by a channel owner, who on a self-service platform is an
// untrusted party.
var allowPrivateWebhookTargets = os.Getenv("WEBHOOK_ALLOW_PRIVATE") == "1"

// blockPrivateAddress refuses to connect anywhere that is not a public address.
//
// The webhook URL comes from the per-channel webhook_url setting, writable by
// any channel owner. Without this an owner could point it at 169.254.169.254 or
// at the platform's own loopback interface and have the server issue requests
// from inside its trust boundary, using the logged response code as an internal
// port scanner.
func blockPrivateAddress(network, address string, _ syscall.RawConn) error {
	if allowPrivateWebhookTargets {
		return nil
	}
	if network != "tcp4" && network != "tcp6" && network != "tcp" {
		return fmt.Errorf("webhook: refusing non-tcp network %q", network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook: unparseable address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webhook: unresolvable address %q", host)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("webhook: refusing to connect to non-public address %s", ip)
	}
	return nil
}

// isPublicIP reports whether ip is routable on the public internet.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// Carrier-grade NAT (100.64.0.0/10) is not covered by IsPrivate but is just
	// as much somebody's internal network.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be judged on the mapped address.
	if ip4 := ip.To4(); ip4 != nil && !ip.Equal(ip4) {
		return isPublicIP(ip4)
	}
	return true
}

type WebhookPayload struct {
	Action      string    `json:"action"`
	Message     Message   `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	VerifyToken string    `json:"verifyToken"`
}

func SendWebhook(ctx context.Context, slug string, action string, message *Message) {
	// Load per-channel config for webhook settings
	chCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cfg := getChannelConfig(chCtx, slug)
	if cfg.WebhookURL == "" {
		return
	}

	// Operator-level kill switch: the super admin can withdraw webhooks from a
	// tenant regardless of what the owner has configured. The channel HAS a
	// webhook configured at this point, so going silent without a trace would
	// make the kill switch undiagnosable.
	if ch, err := dbGetChannel(chCtx, slug); err == nil && !ch.Features.Webhook {
		log.Printf("webhook suppressed for %s: feature disabled by operator\n", slug)
		return
	}

	// The webhook goes to wherever the channel owner points it, so an operator
	// post must reach it re-labelled like everywhere else. A copy: the caller's
	// message is shared with the push notification running alongside.
	msg := *message
	currentOperators().anonymiseOperatorAuthor(&msg)

	payload := WebhookPayload{
		Action:      action,
		Message:     msg,
		Timestamp:   time.Now(),
		VerifyToken: cfg.VerifyToken,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error converting webhook data to JSON: %v\n", err)
		return
	}

	httpCtx, httpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer httpCancel()

	// Scheme check before the socket guard: a non-http scheme would never reach
	// the dialler, and file:// or gopher:// have no business here.
	if u, err := url.Parse(cfg.WebhookURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		log.Printf("webhook for %s: refusing URL with unsupported scheme: %q\n", slug, cfg.WebhookURL)
		return
	}

	req, err := http.NewRequestWithContext(httpCtx, "POST", cfg.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error creating webhook request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TheChannel-Webhook")

	resp, err := webhookClient.Do(req)
	if err != nil {
		log.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Sent webhook for action '%s' on message %d. Response code: %d\n",
		action, message.ID, resp.StatusCode)
}
