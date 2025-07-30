package main

import (
	"encoding/json"
	"net/http"
)

type AdsSettings struct {
	Src   string `json:"src"`
	Width int64  `json:"width"`
}

func getAdsSettings(w http.ResponseWriter, r *http.Request) {
	settings := AdsSettings{
		Src:   settingConfig.AdSrc,
		Width: settingConfig.AdWidth,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
