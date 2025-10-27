package main

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"slices"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/appleboy/go-fcm"
)

type FirebaseConfig struct {
	ApiKey            string `json:"apiKey"`
	AuthDomain        string `json:"authDomain"`
	ProjectId         string `json:"projectId"`
	StorageBucket     string `json:"storageBucket"`
	MessagingSenderId string `json:"messagingSenderId"`
	AppId             string `json:"appId"`
	MeasurementId     string `json:"measurementId"`
}

type FcmJsonConfing struct {
	Type                    string `json:"type"`
	ProjectId               string `json:"project_id"`
	PrivateKeyId            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientId                string `json:"client_id"`
	AuthUri                 string `json:"auth_uri"`
	TokenUri                string `json:"token_uri"`
	AuthProviderX509CertUrl string `json:"auth_provider_x509_cert_url"`
	ClientX509CertUrl       string `json:"client_x509_cert_url"`
	UniverseDomain          string `json:"universe_domain"`
}
type NotificationsConfig struct {
	EnableNotifications bool           `json:"enableNotifications"`
	VAPID               string         `json:"vapid"`
	FirebaseConfig      FirebaseConfig `json:"firebaseConfig"`
}

func getNotificationsConfig(w http.ResponseWriter, r *http.Request) {
	response := NotificationsConfig{
		EnableNotifications: settingConfig.OnNotification,
		VAPID:               settingConfig.VAPID,
		FirebaseConfig: FirebaseConfig{
			ApiKey:            settingConfig.FcmApiKey,
			AuthDomain:        settingConfig.FcmAuthDomain,
			ProjectId:         settingConfig.FcmProjectId,
			StorageBucket:     settingConfig.FcmStorageBucket,
			MessagingSenderId: settingConfig.FcmMessagingSenderId,
			AppId:             settingConfig.FcmAppId,
			MeasurementId:     settingConfig.FcmMeasurementId,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

const firebaseMessagingSW = `
importScripts(
    "https://www.gstatic.com/firebasejs/11.4.0/firebase-app-compat.js"
);
importScripts(
    "https://www.gstatic.com/firebasejs/11.4.0/firebase-messaging-compat.js"
);

const firebaseConfig = {
    apiKey: "{{.FcmApiKey}}",
    authDomain: "{{.FcmAuthDomain}}",
    projectId: "{{.FcmProjectId}}",
    storageBucket: "{{.FcmStorageBucket}}",
    messagingSenderId: "{{.FcmMessagingSenderId}}",
    appId: "{{.FcmAppId}}",
    measurementId: "{{.FcmMeasurementId}}"
};

const app = firebase.initializeApp(firebaseConfig);
const messaging = firebase.messaging();

messaging.onBackgroundMessage((payload) => {
    self.registration.showNotification(payload.data?.title, {
        body: payload.data?.body,
        data: {
            url: payload.data?.url
        },
    });
});

self.addEventListener('notificationclick', (event) => {
    const url = event.notification.data.url;
    if (url) event.waitUntil(clients.openWindow(url));
});
`

func getFirebaseMessagingSW(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	tmpl, err := template.New("firebaseSW").Parse(firebaseMessagingSW)
	if err != nil {
		http.Error(w, "Failed to generate service worker", http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, map[string]string{
		"FcmApiKey":            settingConfig.FcmApiKey,
		"FcmAuthDomain":        settingConfig.FcmAuthDomain,
		"FcmProjectId":         settingConfig.FcmProjectId,
		"FcmStorageBucket":     settingConfig.FcmStorageBucket,
		"FcmMessagingSenderId": settingConfig.FcmMessagingSenderId,
		"FcmAppId":             settingConfig.FcmAppId,
		"FcmMeasurementId":     settingConfig.FcmMeasurementId,
	})
	if err != nil {
		http.Error(w, "Failed to generate service worker", http.StatusInternalServerError)
		return
	}
}

func subscribeNotifications(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.Token == "" || len(req.Token) < 50 || len(req.Token) > 300 {
		http.Error(w, "Invalid token ", http.StatusBadRequest)
		return
	}

	if err := addSubscription(req.Token); err != nil {
		http.Error(w, "Failed to subscribe to notifications", http.StatusInternalServerError)
		return
	}

	var response Response
	response.Success = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func pushFcmMessage(m *Message) {
	if !settingConfig.OnNotification {

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	list, err := getSubcriptionsList()
	if err != nil {
		log.Println("Failed to get subscription list:", err)
		return
	}

	if len(list) == 0 {
		log.Println("No subscriptions found, skipping FCM push")
		return
	}

	channelName, err := getChannelDetails(ctx)
	if err != nil {
		log.Println("Failed to get channel details:", err)
		return
	}

	fcmSet := &FcmJsonConfing{
		Type:                    settingConfig.FcmJson.Type,
		ProjectId:               settingConfig.FcmJson.ProjectId,
		PrivateKeyId:            settingConfig.FcmJson.PrivateKeyId,
		PrivateKey:              settingConfig.FcmJson.PrivateKey,
		ClientEmail:             settingConfig.FcmJson.ClientEmail,
		ClientId:                settingConfig.FcmJson.ClientId,
		AuthUri:                 settingConfig.FcmJson.AuthUri,
		TokenUri:                settingConfig.FcmJson.TokenUri,
		AuthProviderX509CertUrl: settingConfig.FcmJson.AuthProviderX509CertUrl,
		ClientX509CertUrl:       settingConfig.FcmJson.ClientX509CertUrl,
		UniverseDomain:          settingConfig.FcmJson.UniverseDomain,
	}

	fcmSetJson, err := json.Marshal(fcmSet)
	if err != nil {
		log.Println("Failed to marshal FCM credentials:", err)
		return
	}

	client, err := fcm.NewClient(
		ctx,
		fcm.WithCredentialsJSON(fcmSetJson),
	)
	if err != nil {
		log.Println("Failed to create FCM client:", err)
		return
	}

	data := map[string]string{
		"url":   settingConfig.ProjectDomain,
		"title": channelName["name"],
		"body":  m.Text,
	}

	for chunk := range slices.Chunk(list, 500) {
		message := &messaging.MulticastMessage{
			Tokens: chunk,
			Data:   data,
		}

		r, err := client.SendMulticast(ctx, message)
		if err != nil {
			log.Println("Failed to send push notification:", err)
			return
		}
		log.Printf("Push notification sent to %d tokens: \n", r.SuccessCount)
		log.Printf("Failed to send to %d tokens: \n", r.FailureCount)
		// for _, resp := range r.Responses {
		// 	log.Printf("Response: %s, Error: %v\n", resp.MessageID, resp.Error)
		// }
	}
}
