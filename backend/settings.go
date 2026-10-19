package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/icza/dyno"
)

const (
	// defaultMaxFileSizeMB is used when a channel has no (or an invalid) max_file_size.
	defaultMaxFileSizeMB int64 = 100
	// maxAllowedFileSizeMB is the absolute server-side ceiling a channel owner
	// cannot raise, since the whole upload is buffered in memory.
	maxAllowedFileSizeMB int64 = 512
)

type ReplaceRegex struct {
	Pattern *regexp.Regexp
	Replace string
}

type SettingConfig struct {
	AdSrc                  string
	AdWidth                int64
	RegexReplace           []*ReplaceRegex
	WebhookURL             string
	VerifyToken            string
	ApiSecretKey           string
	RootStaticFolder       string
	OnNotification         bool
	VAPID                  string
	FcmApiKey              string
	FcmAuthDomain          string
	FcmProjectId           string
	FcmStorageBucket       string
	FcmMessagingSenderId   string
	FcmAppId               string
	FcmMeasurementId       string
	ProjectDomain          string
	FcmJson                *FcmJsonConfing
	MaxFileSize            int64
	CustomTitle            string
	MagnetEnabled          bool
	MagnetSnippet          string
	MagnetMode             string
	MagnetPerMessages      int64
	MagnetMinTimeSeconds   int64
	MagnetPerSeconds       int64
	MagnetMinMessagesSince int64
	AnalyticsHead          string
	TinyPngApiKey          string
	// The bare tokens of the search consoles' ownership meta tags (seo.go).
	GoogleSiteVerification string
	BingSiteVerification   string
}

type Setting struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// settingConfig is the global config (FCM/VAPID/global notifications). It is
// replaced wholesale by an HTTP handler, so every access goes through the
// accessors below; readers must take one snapshot per function rather than
// dereferencing the global repeatedly, or a swap mid-read splices two configs.
var (
	settingMu     sync.RWMutex
	settingConfig *SettingConfig
)

func getGlobalConfig() *SettingConfig {
	settingMu.RLock()
	defer settingMu.RUnlock()
	return settingConfig
}

func setGlobalConfigCache(c *SettingConfig) {
	settingMu.Lock()
	settingConfig = c
	settingMu.Unlock()
}

type Settings []Setting

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := dbGetGlobalSettings(ctx)
	if err != nil {
		panic("Failed to load settings from database: " + err.Error())
	}

	setGlobalConfigCache(s.ToConfig())
}

func (s *Settings) ToConfig() *SettingConfig {
	config := &SettingConfig{}
	config.FcmJson = &FcmJsonConfing{}

	if rootStaticFolder != "" {
		config.RootStaticFolder = rootStaticFolder
	} else {
		config.RootStaticFolder = "/usr/share/ng"
	}

	config.MaxFileSize = defaultMaxFileSizeMB

	for _, setting := range *s {
		switch setting.Key {
		case "ad-iframe-src":
			// Only an http(s) URL may be framed. The value is bound to an
			// <iframe src> by every viewer's browser, and a javascript: URL
			// there runs in the app's own origin — with the viewer's session.
			if v := strings.TrimSpace(setting.GetString()); isFramableURL(v) {
				config.AdSrc = v
			} else if v != "" {
				warnOnce("ad-iframe-src:"+v, "ad-iframe-src: %q is not an http(s) URL, ignored", v)
			}

		case "ad-iframe-width":
			config.AdWidth = setting.GetInt()

		case "webhook_url":
			config.WebhookURL = setting.GetString()

		case "webhook_verify_token":
			config.VerifyToken = setting.GetString()

		case "api_secret_key":
			config.ApiSecretKey = setting.GetString()

		case "regex-replace":
			if r := setting.GetString(); r != "" {
				pat, rep, ok := splitRegexRule(r)
				switch {
				case !ok:
					warnOnce("regex-sep:"+r, "regex-replace: rule %q has no '#' separator, ignored", r)
				case pat == "":
					// An empty pattern matches at every position, so the
					// replacement would be inserted between every two
					// characters of every message from then on.
					warnOnce("regex-empty:"+r, "regex-replace: rule %q has an empty pattern, ignored", r)
				default:
					if re, err := regexp.Compile(pat); err == nil {
						config.RegexReplace = append(config.RegexReplace, &ReplaceRegex{
							Pattern: re,
							Replace: rep,
						})
					} else {
						log.Printf("regex-replace: bad pattern %q: %v\n", pat, err)
					}
				}
			}

		case "on_notification":
			config.OnNotification = setting.GetBool()

		case "vapid":
			config.VAPID = setting.GetString()

		case "fcm_api_key":
			config.FcmApiKey = setting.GetString()

		case "fcm_auth_domain":
			config.FcmAuthDomain = setting.GetString()

		case "fcm_project_id":
			config.FcmProjectId = setting.GetString()

		case "fcm_storage_bucket":
			config.FcmStorageBucket = setting.GetString()

		case "fcm_messaging_sender_id":
			config.FcmMessagingSenderId = setting.GetString()

		case "fcm_app_id":
			config.FcmAppId = setting.GetString()

		case "fcm_measurement_id":
			config.FcmMeasurementId = setting.GetString()

		case "project_domain":
			config.ProjectDomain = setting.GetString()

		case "max_file_size":
			// A zero/unparseable or oversized value keeps the default instead of
			// bricking uploads or letting an owner exhaust server memory.
			if v := setting.GetInt(); v > 0 && v <= maxAllowedFileSizeMB {
				config.MaxFileSize = v
			}

		case "custom_title":
			config.CustomTitle = setting.GetString()

		case "fcm_json_type":
			config.FcmJson.Type = setting.GetString()

		case "fcm_json_project_id":
			config.FcmJson.ProjectId = setting.GetString()

		case "fcm_json_private_key_id":
			config.FcmJson.PrivateKeyId = setting.GetString()

		case "fcm_json_private_key":
			config.FcmJson.PrivateKey = strings.ReplaceAll(setting.GetString(), "\\n", "\n")

		case "fcm_json_client_email":
			config.FcmJson.ClientEmail = setting.GetString()

		case "fcm_json_client_id":
			config.FcmJson.ClientId = setting.GetString()

		case "fcm_json_auth_uri":
			config.FcmJson.AuthUri = setting.GetString()

		case "fcm_json_token_uri":
			config.FcmJson.TokenUri = setting.GetString()

		case "fcm_json_auth_provider_x509_cert_url":
			config.FcmJson.AuthProviderX509CertUrl = setting.GetString()

		case "fcm_json_client_x509_cert_url":
			config.FcmJson.ClientX509CertUrl = setting.GetString()

		case "fcm_json_universe_domain":
			config.FcmJson.UniverseDomain = setting.GetString()

		case "magnet_enabled":
			config.MagnetEnabled = setting.GetBool()

		case "magnet_snippet":
			config.MagnetSnippet = setting.GetString()

		case "magnet_mode":
			config.MagnetMode = setting.GetString()

		case "magnet_per_messages":
			config.MagnetPerMessages = setting.GetInt()

		case "magnet_min_time_seconds":
			config.MagnetMinTimeSeconds = setting.GetInt()

		case "magnet_per_seconds":
			config.MagnetPerSeconds = setting.GetInt()

		case "magnet_min_messages_since":
			config.MagnetMinMessagesSince = setting.GetInt()

		case "analytics_head":
			config.AnalyticsHead = setting.GetString()

		case "tinypng_api_key":
			config.TinyPngApiKey = setting.GetString()

		case "google_site_verification":
			config.GoogleSiteVerification = verificationToken(setting.GetString())

		case "bing_site_verification":
			config.BingSiteVerification = verificationToken(setting.GetString())
		}
	}

	return config
}

// validateSettings rejects values ToConfig would silently ignore. Every
// sanity check used to live only at read time, as a log line: a regex RE2
// cannot compile, a max_file_size over the ceiling, an ad or webhook address
// that is not an http(s) URL — all "saved" with a success toast and then did
// nothing, with the form still displaying the value.
func validateSettings(s *Settings) error {
	for _, setting := range *s {
		switch setting.Key {
		case "regex-replace":
			r := setting.GetString()
			if r == "" {
				continue
			}
			pat, _, ok := splitRegexRule(r)
			if !ok || pat == "" {
				return fmt.Errorf("regex-replace: a rule needs a pattern, '#' and a replacement")
			}
			if _, err := regexp.Compile(pat); err != nil {
				return fmt.Errorf("regex-replace: invalid pattern: %v", err)
			}
		case "ad-iframe-src":
			if v := strings.TrimSpace(setting.GetString()); v != "" && !isFramableURL(v) {
				return fmt.Errorf("%s: must be an absolute http(s) URL", setting.Key)
			}
		case "webhook_url":
			if v := strings.TrimSpace(setting.GetString()); v != "" && !isHTTPURL(v) {
				return fmt.Errorf("%s: must be an absolute http(s) URL", setting.Key)
			}
		case "api_secret_key":
			// The import key is all that guards an unauthenticated route
			// (addNewPost). Its failed-attempt limiter slows guessing, but a
			// one- or two-character key — the docs once showed "1" — still
			// falls in minutes. Only a value being set is checked (see
			// changedSettings), so a short legacy key keeps working until the
			// owner replaces it, and clearing the key stays allowed: an empty
			// key fails closed. The text is matched by the frontend, which
			// shows it in Hebrew, so it must not change.
			if v := strings.TrimSpace(setting.GetString()); v != "" && utf8.RuneCountInString(v) < 16 {
				return fmt.Errorf("api_secret_key must be at least 16 characters")
			}
		case "google_site_verification", "bing_site_verification":
			// Lands in every page's <head>; only a token (or the whole tag
			// the console hands out, from which the token is taken) is
			// accepted.
			if v := strings.TrimSpace(setting.GetString()); v != "" && verificationToken(v) == "" {
				return fmt.Errorf("%s: not a verification code", setting.Key)
			}
		case "max_file_size":
			// The form posts a number; an empty field (nil or "") means default.
			if setting.Value == nil {
				continue
			}
			if str, isStr := setting.Value.(string); isStr && strings.TrimSpace(str) == "" {
				continue
			}
			if n := setting.GetInt(); n <= 0 || n > maxAllowedFileSizeMB {
				return fmt.Errorf("max_file_size: must be between 1 and %d MB", maxAllowedFileSizeMB)
			}
		}
	}
	return nil
}

// changedSettings returns the entries of next whose value is new or differs
// from stored. Validation runs on those only: every tab re-posts the whole
// list, so a legacy value saved before validation existed (an old hashtag
// rule with an empty pattern, a max_file_size of 0) made a tab that cannot
// even display it unsaveable, with nothing on that screen to fix.
func changedSettings(stored, next Settings) Settings {
	old := make(map[string]string, len(stored))
	for _, s := range stored {
		b, _ := json.Marshal(s.Value)
		old[s.Key+"\x00"+string(b)] = ""
	}
	var out Settings
	for _, s := range next {
		b, _ := json.Marshal(s.Value)
		if _, same := old[s.Key+"\x00"+string(b)]; !same {
			out = append(out, s)
		}
	}
	return out
}

// warnedConfig remembers which configuration warnings were already logged:
// ToConfig runs on every post, upload and webhook, so one legacy bad rule
// used to write the same line thousands of times.
var warnedConfig sync.Map

func warnOnce(key, format string, args ...any) {
	if _, seen := warnedConfig.LoadOrStore(key, struct{}{}); !seen {
		log.Printf(format+"\n", args...)
	}
}

// isFramableURL accepts what isHTTPURL does plus a protocol-relative URL
// (//host/path), which is a valid iframe source that existing channels use
// and resolves to the page's own scheme.
func isFramableURL(u string) bool {
	if strings.HasPrefix(u, "//") {
		return isHTTPURL("https:" + u)
	}
	return isHTTPURL(u)
}

// isHTTPURL reports whether u is an absolute http or https URL with a host.
func isHTTPURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return !strings.ContainsAny(u, " \t\r\n\"'<>")
	}
	return false
}

func (s *Setting) GetBool() bool {
	b, _ := dyno.GetBoolean(s.Value)
	return b
}

func (s *Setting) GetString() string {
	str, _ := dyno.GetString(s.Value)
	return str
}

func (s *Setting) GetInt() int64 {
	i, _ := dyno.GetInteger(s.Value)
	return i
}

// getChannelConfig loads per-channel settings from DB and returns a SettingConfig
func getChannelConfig(ctx context.Context, slug string) *SettingConfig {
	s, err := dbGetSettings(ctx, slug)
	if err != nil {
		// Route the failure through the same defaulting logic, or MaxFileSize
		// lands at 0 and MaxBytesReader rejects every upload as "too large".
		return (&Settings{}).ToConfig()
	}
	return s.ToConfig()
}

// Per-channel settings handlers (owner level)
func setSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var newSettings Settings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		http.Error(w, "error decoding settings", http.StatusBadRequest)
		return
	}
	stored, _ := dbGetSettings(ctx, slug)
	changed := changedSettings(stored, newSettings)
	if err := validateSettings(&changed); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := dbSetSettings(ctx, slug, &newSettings); err != nil {
		http.Error(w, "error saving settings", http.StatusInternalServerError)
		return
	}

	res := Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func getSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	s, err := dbGetSettings(ctx, slug)
	if err != nil {
		http.Error(w, "error getting settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// Global settings handlers (super admin level)
func getGlobalSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := dbGetGlobalSettings(ctx)
	if err != nil {
		http.Error(w, "error getting global settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func setGlobalSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newSettings Settings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		http.Error(w, "error decoding settings", http.StatusBadRequest)
		return
	}
	stored, _ := dbGetGlobalSettings(ctx)
	changed := changedSettings(stored, newSettings)
	if err := validateSettings(&changed); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := dbSetGlobalSettings(ctx, &newSettings); err != nil {
		http.Error(w, "error saving global settings", http.StatusInternalServerError)
		return
	}

	setGlobalConfigCache(newSettings.ToConfig())
	notifyGlobalSettingsChanged()

	res := Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// The global config is a per-process cache, and DEPLOY.md contemplates more
// than one backend replica: a save on one of them used to leave the others
// serving the old FCM credentials, title and analytics tag until restart.
// Same mechanism as the privileges map — announce on pub/sub, refresh
// periodically as the backstop.
const (
	globalSettingsReloadChannel   = "settings:reload"
	globalSettingsRefreshInterval = 60 * time.Second
)

func reloadGlobalConfig() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := dbGetGlobalSettings(ctx)
	if err != nil {
		return err
	}
	setGlobalConfigCache(s.ToConfig())
	return nil
}

func notifyGlobalSettingsChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Publish(ctx, globalSettingsReloadChannel, "reload").Err(); err != nil {
		log.Printf("settings: announce reload: %v\n", err)
	}
}

func startGlobalSettingsRefresh() {
	go func() {
		ticker := time.NewTicker(globalSettingsRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := reloadGlobalConfig(); err != nil {
				log.Printf("settings: periodic reload: %v\n", err)
			}
		}
	}()
	go func() {
		for {
			sub := rdb.Subscribe(context.Background(), globalSettingsReloadChannel)
			for range sub.Channel() {
				if err := reloadGlobalConfig(); err != nil {
					log.Printf("settings: reload on announcement: %v\n", err)
				}
			}
			sub.Close()
			time.Sleep(5 * time.Second)
		}
	}()
}

// splitRegexRule splits a stored "<pattern>#<replacement>" rule at the first
// '#' that is not escaped as "\#".
//
// The separator is also an ordinary regex character, and a hashtag rule is
// the obvious thing to write: "#(\S+)#**#$1**" split at its first '#' gave an
// EMPTY pattern, which garbled every message posted afterwards (see ToConfig).
// The settings form writes '\#' for a '#' inside the pattern, and RE2 reads
// "\#" as a literal '#', so the escaped pattern compiles unchanged. The
// replacement half may contain '#' freely.
func splitRegexRule(rule string) (pattern, replacement string, ok bool) {
	for i := 0; i < len(rule); i++ {
		switch rule[i] {
		case '\\':
			i++ // whatever follows a backslash is escaped, including '#'
		case '#':
			return rule[:i], rule[i+1:], true
		}
	}
	return "", "", false
}
