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
	MaxFileSize             int64
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
