package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

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
	AdSrc                   string
	AdWidth                 int64
	RequireAuth             bool
	RequireAuthForViewFiles bool
	RegexReplace            []*ReplaceRegex
	WebhookURL              string
	VerifyToken             string
	ApiSecretKey            string
	RootStaticFolder        string
	CountViews              bool
	OnNotification          bool
	VAPID                   string
	FcmApiKey               string
	FcmAuthDomain           string
	FcmProjectId            string
	FcmStorageBucket        string
	FcmMessagingSenderId    string
	FcmAppId                string
	FcmMeasurementId        string
	ProjectDomain           string
	FcmJson                 *FcmJsonConfing
	MaxFileSize             int64
	CustomTitle             string
	ContactUs               string
	MagnetEnabled           bool
	MagnetSnippet           string
	MagnetMode              string
	MagnetPerMessages       int64
	MagnetMinTimeSeconds    int64
	MagnetPerSeconds        int64
	MagnetMinMessagesSince  int64
	MagnetApiKey            string
	AnalyticsHead           string
	TinyPngApiKey           string
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
			config.AdSrc = setting.GetString()

		case "ad-iframe-width":
			config.AdWidth = setting.GetInt()

		case "require_auth":
			config.RequireAuth = setting.GetBool()

		case "require_auth_for_view_files":
			config.RequireAuthForViewFiles = setting.GetBool()

		case "webhook_url":
			config.WebhookURL = setting.GetString()

		case "webhook_verify_token":
			config.VerifyToken = setting.GetString()

		case "api_secret_key":
			config.ApiSecretKey = setting.GetString()

		case "count_views":
			config.CountViews = setting.GetBool()

		case "regex-replace":
			// The rule is encoded as "<pattern>#<replacement>" and the UI splits
			// it on the FIRST '#', so splitting on every '#' here would silently
			// drop any rule that legitimately contains one (e.g. a hashtag).
			if r := setting.GetString(); r != "" {
				if i := strings.Index(r, "#"); i >= 0 {
					pat, rep := r[:i], r[i+1:]
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

		case "contact_us":
			config.ContactUs = setting.GetString()

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

		case "magnet_api_key":
			config.MagnetApiKey = setting.GetString()

		case "analytics_head":
			config.AnalyticsHead = setting.GetString()

		case "tinypng_api_key":
			config.TinyPngApiKey = setting.GetString()
		}
	}

	return config
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

	if err := dbSetGlobalSettings(ctx, &newSettings); err != nil {
		http.Error(w, "error saving global settings", http.StatusInternalServerError)
		return
	}

	setGlobalConfigCache(newSettings.ToConfig())

	res := Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
