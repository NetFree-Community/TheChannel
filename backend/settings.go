package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/icza/dyno"
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
	FrameAncestorsDomains   string
	AllowedOrigins          []string
	validateOrigin          bool
}

type Setting struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

var settingConfig *SettingConfig

type Settings []Setting

func init() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := dbGetSettings(ctx)
	if err != nil {
		panic("Failed to load settings from database: " + err.Error())
	}

	settingConfig = s.ToConfig()
}

func (s *Settings) ToConfig() *SettingConfig {
	config := &SettingConfig{}
	config.FcmJson = &FcmJsonConfing{}

	if rootStaticFolder != "" {
		config.RootStaticFolder = rootStaticFolder
	} else {
		config.RootStaticFolder = "/usr/share/ng"
	}

	config.MaxFileSize = 100

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
			if r := setting.GetString(); r != "" {
				parts := strings.Split(r, "#")
				if len(parts) == 2 {
					if r, err := regexp.Compile(parts[0]); err == nil {
						config.RegexReplace = append(config.RegexReplace, &ReplaceRegex{
							Pattern: r,
							Replace: parts[1],
						})
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
			config.MaxFileSize = setting.GetInt()

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

		case "frame_ancestors_domains":
			config.FrameAncestorsDomains = setting.GetString()
		case "allowed_origins":
            originsStr := setting.GetString()
            if originsStr != "" {
                origins := strings.Split(originsStr, ",")
                for _, origin := range origins {
                    config.AllowedOrigins = append(config.AllowedOrigins, strings.TrimSpace(origin))
				}
			}
		case "validate_Origin":
			config.validateOrigin = setting.GetBool()
		}
	}

	return config
}

func (s *Setting) GetBool() bool {
	b, _ := dyno.GetBoolean(s.Value)
	return b
}

func (s *Setting) GetString()  string {
	str, _ := dyno.GetString(s.Value)
	return str
}

func (s *Setting) GetInt() int64 {
	i, _ := dyno.GetInteger(s.Value)
	return i
}

func setSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newSettings Settings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		http.Error(w, "error decoding settings", http.StatusBadRequest)
		return
	}

	if err := dbSetSettings(ctx, &newSettings); err != nil {
		http.Error(w, "error saving settings", http.StatusInternalServerError)
		return
	}

	settingConfig = newSettings.ToConfig()

	res := Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func getSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, err := dbGetSettings(ctx)
	if err != nil {
		http.Error(w, "error getting settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}
