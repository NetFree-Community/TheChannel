package main

import (
	"encoding/json"
	"net/http"
)

type AdsSettings struct {
	Src string `json:"src"`
}

func getAdsSettings(w http.ResponseWriter, r *http.Request) {
	settings := AdsSettings{
		Src: settingConfig.AdSrc,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
