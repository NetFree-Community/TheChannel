package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPushPreview(t *testing.T) {
	cases := []struct{ in, want string }{
		{"שלום **עולם**", "שלום עולם"},
		{"[image-embedded#640x480](/api/channel/x/files/abc)\nכותרת", "כותרת"},
		{"ראו [כאן](https://example.com/a?b=c) עכשיו", "ראו כאן עכשיו"},
		{"[quote-embedded#]ציטוט\n\n\nשורה", "ציטוט שורה"},
		{"  ", ""},
	}
	for _, c := range cases {
		if got := pushPreview(c.in); got != c.want {
			t.Errorf("pushPreview(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("א", 2*maxPushBodyRunes)
	got := pushPreview(long)
	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n != maxPushBodyRunes+1 {
		t.Errorf("preview length = %d runes, want %d + ellipsis", n, maxPushBodyRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated preview should end with an ellipsis")
	}
}

func TestTruncateRunesKeepsShortInput(t *testing.T) {
	if got := truncateRunes("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestContentDispositionAttachment(t *testing.T) {
	got := contentDispositionAttachment("דוח שנתי 2024.pdf")
	want := `attachment; filename="___ ____ 2024.pdf"; filename*=UTF-8''%D7%93%D7%95%D7%97%20%D7%A9%D7%A0%D7%AA%D7%99%202024.pdf`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	// A space must never become "+": browsers do not decode it in filename*.
	if strings.Contains(rfc5987Encode("a b"), "+") {
		t.Errorf("space encoded as +")
	}
	if got := contentDispositionAttachment(`x"y\z.txt`); !strings.Contains(got, `filename="x_y_z.txt"`) {
		t.Errorf("quote/backslash not neutralised in fallback: %s", got)
	}
	if got := contentDispositionAttachment("שלום"); !strings.Contains(got, `filename="file"`) {
		t.Errorf("all-non-ascii name should fall back to a placeholder: %s", got)
	}
}

func TestTrimToCountsCharacters(t *testing.T) {
	// 5 Hebrew letters = 10 bytes; the limit is in characters, like the
	// form's maxlength, so all five fit under a cap of 5.
	if got := trimTo("אבגדה", 5); got != "אבגדה" {
		t.Errorf("got %q, want the whole string", got)
	}
	got := trimTo("אבגדה", 3)
	if !utf8.ValidString(got) {
		t.Fatalf("trimTo produced invalid UTF-8: %q", got)
	}
	if got != "אבג" {
		t.Errorf("got %q, want %q", got, "אבג")
	}
	if got := trimTo("  abc  ", 10); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := trimTo("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestRemoteHost(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5678":        "1.2.3.4",
		"1.2.3.4":             "1.2.3.4",
		"[2001:db8::1]:443":   "2001:db8::1",
		"2001:db8::1":         "2001:db8::1",
		"2001:db8::1:2:3:4:5": "2001:db8::1:2:3:4:5",
		"[2001:db8::1]":       "2001:db8::1",
	}
	for in, want := range cases {
		if got := remoteHost(in); got != want {
			t.Errorf("remoteHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsHTTPURL(t *testing.T) {
	ok := []string{"https://ads.example.com/banner.html", "http://a.b/c?d=1", "HTTPS://X.Y/"}
	for _, u := range ok {
		if !isHTTPURL(u) {
			t.Errorf("isHTTPURL(%q) = false, want true", u)
		}
	}
	bad := []string{"", "javascript:alert(1)", "javascript://x/%0aalert(1)", "data:text/html,hi",
		"//evil.example/x", "https://", "ftp://a.b/c", "https://a.b/c d", "vbscript:x"}
	for _, u := range bad {
		if isHTTPURL(u) {
			t.Errorf("isHTTPURL(%q) = true, want false", u)
		}
	}
}

func TestIsSafeContactURL(t *testing.T) {
	ok := []string{"", "https://example.com/contact", "http://wa.me/972500000000", "mailto:owner@example.com", "MAILTO:A@b.c"}
	bad := []string{"example.com", "javascript:alert(1)", "mailto:", "https://x y", "data:text/html,hi", "/relative"}
	for _, u := range ok {
		if !isSafeContactURL(u) {
			t.Errorf("%q should be accepted", u)
		}
	}
	for _, u := range bad {
		if isSafeContactURL(u) {
			t.Errorf("%q should be refused", u)
		}
	}
}

func TestSplitRegexRule(t *testing.T) {
	cases := []struct {
		rule, pat, rep string
		ok             bool
	}{
		{`(.*?\!)(.*)#**$1**$2`, `(.*?\!)(.*)`, `**$1**$2`, true},
		// A '#' inside the pattern is escaped by the form; the replacement
		// half keeps its own '#' characters as they are.
		{`\#(\S+)#**#$1**`, `\#(\S+)`, `**#$1**`, true},
		// An escaped backslash before the separator is not an escape of it.
		{`a\\#b`, `a\\`, `b`, true},
		{`no-separator`, ``, ``, false},
		{`#rep`, ``, `rep`, true},
	}
	for _, c := range cases {
		pat, rep, ok := splitRegexRule(c.rule)
		if pat != c.pat || rep != c.rep || ok != c.ok {
			t.Errorf("splitRegexRule(%q) = (%q, %q, %v), want (%q, %q, %v)", c.rule, pat, rep, ok, c.pat, c.rep, c.ok)
		}
	}
	// The escaped form must compile to a literal '#'.
	re, err := regexp.Compile(`\#(\S+)`)
	if err != nil {
		t.Fatalf("escaped '#' does not compile: %v", err)
	}
	if got := re.ReplaceAllString("see #tag now", "**#$1**"); got != "see **#tag** now" {
		t.Errorf("hashtag rule: got %q", got)
	}
}

func TestNormalizeUsersMergesCaseDuplicates(t *testing.T) {
	users := normalizeUsers([]User{
		{Email: "Owner@Example.com", ChannelRoles: map[string]ChannelRole{"a": RoleWriter}},
		{Email: " owner@example.com ", PublicName: "Owner", ChannelRoles: map[string]ChannelRole{"a": RoleOwner, "b": RoleModerator}},
		{Email: "", GlobalRole: RoleSuperAdmin},
		{Email: "Admin@Example.com", GlobalRole: RoleSuperAdmin},
	})
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2: %+v", len(users), users)
	}
	o := users[0]
	if o.Email != "owner@example.com" || o.PublicName != "Owner" {
		t.Errorf("merged owner: %+v", o)
	}
	if o.ChannelRoles["a"] != RoleOwner || o.ChannelRoles["b"] != RoleModerator {
		t.Errorf("merged roles keep the strongest: %+v", o.ChannelRoles)
	}
	if users[1].Email != "admin@example.com" || users[1].GlobalRole != RoleSuperAdmin {
		t.Errorf("admin: %+v", users[1])
	}
}

func TestIsUploadRoute(t *testing.T) {
	if !isUploadRoute("/api/channel/news/admin/upload") {
		t.Error("upload route not recognised")
	}
	for _, p := range []string{"/api/channel/news/admin/new", "/api/support/tickets", "/admin/upload", "/api/channel/x/admin/upload/extra"} {
		if isUploadRoute(p) {
			t.Errorf("%s wrongly exempt", p)
		}
	}
}

func TestIsPlausibleSlug(t *testing.T) {
	for _, ok := range []string{"news", "Legacy_Channel", "a-b-c"} {
		if !isPlausibleSlug(ok) {
			t.Errorf("%q should be looked up", ok)
		}
	}
	for _, bad := range []string{"", "x:messages:5", "a/b", "a b", strings.Repeat("a", 65)} {
		if isPlausibleSlug(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// A protocol-relative iframe source is what some channels saved before the
// URL check existed; it must keep working, while webhooks stay strict.
func TestIsFramableURLAcceptsProtocolRelative(t *testing.T) {
	for _, ok := range []string{"https://ads.example.com/x", "//ads.example.com/banner.html"} {
		if !isFramableURL(ok) {
			t.Errorf("isFramableURL(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"//", "javascript:alert(1)", "/relative", "ftp://x/y", "//host with space/x"} {
		if isFramableURL(bad) {
			t.Errorf("isFramableURL(%q) = true, want false", bad)
		}
	}
	if isHTTPURL("//ads.example.com/banner.html") {
		t.Error("isHTTPURL must stay strict for webhook targets")
	}
}

// Only entries that changed are validated, so a legacy value the form cannot
// even display does not make the whole list unsaveable.
func TestChangedSettingsIgnoresUntouchedLegacyValues(t *testing.T) {
	stored := Settings{
		{Key: "regex-replace", Value: "#(\\S+)#**#$1**"},
		{Key: "max_file_size", Value: float64(0)},
		{Key: "webhook_url", Value: "https://hooks.example.com/a"},
	}
	next := Settings{
		{Key: "regex-replace", Value: "#(\\S+)#**#$1**"},
		{Key: "max_file_size", Value: float64(0)},
		{Key: "webhook_url", Value: "not a url"},
		{Key: "magnet_enabled", Value: true},
	}
	changed := changedSettings(stored, next)
	if len(changed) != 2 || changed[0].Key != "webhook_url" || changed[1].Key != "magnet_enabled" {
		t.Fatalf("changed = %+v, want webhook_url and magnet_enabled", changed)
	}
	if err := validateSettings(&changed); err == nil || !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("expected the new webhook_url to be rejected, got %v", err)
	}
	untouched := changedSettings(stored, stored)
	if len(untouched) != 0 {
		t.Fatalf("re-saving the stored list must validate nothing, got %+v", untouched)
	}
}

// The import key guards an unauthenticated route, and a one-character key
// falls to guessing in minutes despite the failed-attempt limiter. Only a
// value being set is checked, so a short legacy key keeps working until the
// owner replaces it; the error text is what the frontend maps to Hebrew, so
// it is matched exactly.
func TestValidateSettingsRequiresLongAPISecretKey(t *testing.T) {
	short := Settings{{Key: "api_secret_key", Value: "1"}}
	err := validateSettings(&short)
	if err == nil || err.Error() != "api_secret_key must be at least 16 characters" {
		t.Fatalf("short key: got %v", err)
	}
	for _, v := range []string{"", "   ", "0123456789abcdef", strings.Repeat("א", 16)} {
		ok := Settings{{Key: "api_secret_key", Value: v}}
		if err := validateSettings(&ok); err != nil {
			t.Errorf("%q should be accepted: %v", v, err)
		}
	}
	stored := Settings{{Key: "api_secret_key", Value: "1"}}
	if changed := changedSettings(stored, stored); len(changed) != 0 {
		t.Fatalf("an untouched legacy key must not be re-validated, got %+v", changed)
	}
}
