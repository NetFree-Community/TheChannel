package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/boj/redistore"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var secretKey string = os.Getenv("SECRET_KEY")
var store = &redistore.RediStore{}
var cookieName = "channel_session"
var (
	googleOAuthScopes       = "https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
	googleOAuthUrl          = google.Endpoint.AuthURL
	googleOAuthClientId     = os.Getenv("GOOGLE_CLIENT_ID")
	googleOAuthClientSecret = os.Getenv("GOOGLE_CLIENT_SECRET")
)

type Auth struct {
	Code string `json:"code"`
}

type GoogleAuthValues struct {
	GoogleOauthUrl   string `json:"googleOauthUrl"`
	GoogleOauthScope string `json:"googleOauthScope"`
	GoogleClientId   string `json:"googleClientId"`
}

type Session struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	PublicName string     `json:"publicName"`
	Picture    string     `json:"picture,omitempty"`
	Privileges Privileges `json:"privileges,omitempty"`
}

type Response struct {
	Success bool `json:"success"`
}

func getGoogleAuthValues(w http.ResponseWriter, r *http.Request) {
	authValues := GoogleAuthValues{
		GoogleOauthUrl:   googleOAuthUrl,
		GoogleOauthScope: googleOAuthScopes,
		GoogleClientId:   googleOAuthClientId,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(authValues)
}

func login(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var auth Auth

	if err := json.NewDecoder(r.Body).Decode(&auth); err != nil {
		http.Error(w, "error", http.StatusBadRequest)
	}

	if auth.Code == "" {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	origin := r.Header.Get("Origin")
	var googleOAuthConfig = &oauth2.Config{
		ClientID:     googleOAuthClientId,
		ClientSecret: googleOAuthClientSecret,
		RedirectURL:  origin + "/login",
		Endpoint:     google.Endpoint,
	}

	token, err := googleOAuthConfig.Exchange(ctx, auth.Code)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if !token.Valid() {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	var claims jwt.MapClaims
	_, _, err = jwt.NewParser().ParseUnverified(token.Extra("id_token").(string), &claims)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go registeringEmail(claims["email"].(string))

	u, err := getUser(ctx, claims)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	userSession := Session{
		ID:         u.ID,
		Username:   u.Username,
		PublicName: u.PublicName,
		Picture:    claims["picture"].(string),
		Privileges: u.Privileges,
	}

	session, _ := store.Get(r, cookieName)
	session.Values["user"] = userSession
	session.Options.MaxAge = 60 * 60 * 24 * 30 // 30 days
	if err := session.Save(r, w); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

// func isRootUser(s string) bool {
// 	return slices.Contains(adminUsers, s)
// }

func logout(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)

	session.Values["user"] = nil
	session.Options.MaxAge = -1
	err := session.Save(r, w)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func checkLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := store.Get(r, cookieName)

		_, ok := session.Values["user"].(Session)
		if !ok {
			http.Error(w, "User not authenticated", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func checkPrivilege(r *http.Request, privilege Privilege) bool {
	session, _ := store.Get(r, cookieName)

	s, ok := session.Values["user"].(Session)
	if !ok {
		return false
	}

	return s.Privileges[privilege]
}

func getUserInfo(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)
	userInfo, ok := session.Values["user"].(Session)
	if !ok {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)
}

func getUser(ctx context.Context, claims jwt.MapClaims) (*User, error) {
	var user User

	email := claims["email"].(string)
	id, _ := claims.GetSubject() // Google user ID

	if v, ok := privilegesUsers.Load(email); ok {
		user = v.(User)
		if user.ID != id && id != "" {
			user.ID = id
		}
		if user.Username == "" {
			user.Username = claims["name"].(string)
		}
		if user.Email == "" {
			user.Email = email
		}
		if user.PublicName == "" {
			user.PublicName = claims["name"].(string)
		}
		privilegesUsers.Store(email, user)
		users, err := dbGetUsersList(ctx)
		if err != nil && err != redis.Nil {
			return nil, err
		}
		for i, u := range users {
			if u.Email == email {
				users[i] = user
			}
		}
		if err := dbSetUsersList(ctx, users); err != nil {
			return nil, err
		}
	} else {
		privileges := Privileges{}
		if settingConfig.AutoGrantWriterPrivilege {
			privileges[Writer] = true
		}
		
		user = User{
			ID:         id,
			Username:   claims["name"].(string),
			Email:      claims["email"].(string),
			PublicName: claims["name"].(string),
			Privileges: privileges,
		}
		
		privilegesUsers.Store(email, user)
		users, err := dbGetUsersList(ctx)
		if err != nil && err != redis.Nil {
			return nil, err
		}

		users = append(users, user)
		if err := dbSetUsersList(ctx, users); err != nil {
			return nil, err
		}
	}

	return &user, nil
}
