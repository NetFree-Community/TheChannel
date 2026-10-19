package main

import (
	"net"
	"testing"
)

// The webhook URL is written by a channel owner, who on a self-service platform
// is an untrusted party. Without a guard, an owner could point it at the cloud
// metadata service or at the platform's own loopback interface and have the
// server issue requests from inside its trust boundary.
func TestWebhookRefusesNonPublicAddresses(t *testing.T) {
	blocked := []struct{ name, ip string }{
		{"IPv4 loopback", "127.0.0.1"},
		{"IPv4 loopback range", "127.10.20.30"},
		{"cloud metadata service", "169.254.169.254"},
		{"link-local", "169.254.1.1"},
		{"RFC1918 10/8", "10.0.0.5"},
		{"RFC1918 172.16/12", "172.16.31.9"},
		{"RFC1918 192.168/16", "192.168.1.1"},
		{"carrier-grade NAT", "100.64.0.1"},
		{"CGNAT upper bound", "100.127.255.254"},
		{"unspecified", "0.0.0.0"},
		{"multicast", "224.0.0.1"},
		{"IPv6 loopback", "::1"},
		{"IPv6 unique local", "fd00::1"},
		{"IPv6 link-local", "fe80::1"},
		{"IPv6 unspecified", "::"},
		// A mapped address must be judged on the address it maps to, or
		// ::ffff:127.0.0.1 walks straight past an IPv4-only check.
		{"IPv4-mapped loopback", "::ffff:127.0.0.1"},
		{"IPv4-mapped private", "::ffff:10.0.0.1"},
	}
	for _, c := range blocked {
		t.Run("blocked/"+c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("test bug: %q is not an IP", c.ip)
			}
			if isPublicIP(ip) {
				t.Errorf("%s (%s) was treated as public", c.name, c.ip)
			}
			if err := blockPrivateAddress("tcp", net.JoinHostPort(c.ip, "80"), nil); err == nil {
				t.Errorf("dial to %s (%s) was allowed", c.name, c.ip)
			}
		})
	}

	// Ordinary public destinations must keep working — this feature is only
	// useful if real webhooks still deliver.
	allowed := []struct{ name, ip string }{
		{"public IPv4", "93.184.216.34"},
		{"public IPv4 (CGNAT-adjacent)", "100.128.0.1"},
		{"public IPv4 (below CGNAT)", "100.63.255.255"},
		{"public IPv6", "2606:2800:220:1:248:1893:25c8:1946"},
	}
	for _, c := range allowed {
		t.Run("allowed/"+c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("test bug: %q is not an IP", c.ip)
			}
			if !isPublicIP(ip) {
				t.Errorf("%s (%s) was treated as non-public", c.name, c.ip)
			}
			if err := blockPrivateAddress("tcp", net.JoinHostPort(c.ip, "443"), nil); err != nil {
				t.Errorf("dial to %s (%s) was refused: %v", c.name, c.ip, err)
			}
		})
	}
}

// The operator escape hatch exists for a platform running entirely inside a
// trusted network; it must be off unless explicitly set.
func TestWebhookPrivateEscapeHatchIsOffByDefault(t *testing.T) {
	if allowPrivateWebhookTargets {
		t.Error("WEBHOOK_ALLOW_PRIVATE must default to off")
	}
}
