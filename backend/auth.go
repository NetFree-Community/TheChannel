package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/boj/redistore"
	"github.com/icza/dyno"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
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
	ID           string                 `json:"id"`
	Username     string                 `json:"username"`
	Email        string                 `json:"email"`
	PublicName   string                 `json:"publicName"`
	Picture      string                 `json:"picture,omitempty"`
	GlobalRole   GlobalRole             `json:"globalRole,omitempty"`
	ChannelRoles map[string]ChannelRole `json:"channelRoles,omitempty"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()
	var auth Auth

	if err := json.NewDecoder(r.Body).Decode(&auth); err != nil {
		go saveLoginFailedLog("Decode", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	if auth.Code == "" {
		go saveLoginFailedLog("Decode", errors.New("invalid credentials"))
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
		go saveLoginFailedLog("Exchange", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if !token.Valid() {
		go saveLoginFailedLog("Invalid token", errors.New("oauth2 token not valid"))
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	tokenStr, _ := dyno.GetString(token.Extra("id_token"))
	tokenValidator, err := idtoken.NewValidator(ctx)
	if err != nil {
		go saveLoginFailedLog("NewValidator", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	payload, err := tokenValidator.Validate(ctx, tokenStr, googleOAuthClientId)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// The email address is the sole authorization principal (ADMIN_USERS,
	// ownerEmail, channel roles), so an unproven one must not be accepted.
	// Google always emits email_verified alongside email for the scopes we
	// request, so an absent claim is treated as unverified.
	if verified, verr := dyno.GetBoolean(payload.Claims["email_verified"]); verr != nil || !verified {
		go saveLoginFailedLog("unverified email", errors.New("email_verified claim is false or absent"))
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	email, _ := dyno.GetString(payload.Claims["email"])
	go registeringEmail("", email)

	u, err := getUser(ctx, payload.Claims)
	if err != nil {
		go saveLoginFailedLog("getUser", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	picture, _ := dyno.GetString(payload.Claims["picture"])
	userSession := Session{
		ID:           u.ID,
		Username:     u.Username,
		PublicName:   u.PublicName,
		Picture:      picture,
		GlobalRole:   u.GlobalRole,
		ChannelRoles: u.ChannelRoles,
		Email:        u.Email,
	}

	session, _ := store.Get(r, cookieName)
	// Rotate the session identifier before writing the authenticated principal,
	// otherwise an attacker-planted cookie would be upgraded to the victim's
	// session (fixation). redistore mints a fresh ID only when ID is empty.
	if session.ID != "" {
		session.Options.MaxAge = -1
		_ = session.Save(r, w)       // drops session_<oldID> from Redis
		w.Header().Del("Set-Cookie") // discard the expiry cookie; the real one is set below
		session.ID = ""
	}
	session.Values["user"] = userSession
	session.Options.MaxAge = 60 * 60 * 24 * 30 // 30 days
	if err := session.Save(r, w); err != nil {
		go saveLoginFailedLog("sessionSave", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

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

func getUserInfo(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, cookieName)
	userInfo, ok := session.Values["user"].(Session)
	if !ok {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Register the viewer against the channel they entered. This is the only
	// per-channel writer of registered_emails, which backs the channel user
	// count. Returns "" on the global mount, which stays unaffected.
	if slug := channelSlugFromCtx(r); slug != "" {
		go registeringEmail(slug, userInfo.Email)
	}

	// Authorization resolves roles live (see sessionUser); the copy stored in
	// the cookie at login time can be stale, so refresh it before responding
	// or the UI would keep offering actions the backend now rejects.
	userInfo.GlobalRole = ""
	userInfo.ChannelRoles = nil
	if u, ok := sessionUser(r); ok {
		userInfo.GlobalRole = u.GlobalRole
		userInfo.ChannelRoles = u.ChannelRoles
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userInfo)
}

func getUser(ctx context.Context, claims map[string]any) (*User, error) {
	var user User

	email, _ := dyno.GetString(claims["email"])
	if email == "" {
		return nil, errors.New("email not found in claims")
	}
	name, _ := dyno.GetString(claims["name"])
	if name == "" {
		return nil, errors.New("name not found in claims")
	}
	id, _ := dyno.GetString(claims["sub"]) // Google user ID

	if v, ok := privilegesUsers.Load(email); ok {
		user = v.(User)
		if user.ID != id && id != "" {
			user.ID = id
		}
		if user.Username == "" {
			user.Username = name
		}
		if user.Email == "" {
			user.Email = email
		}
		if user.PublicName == "" {
			user.PublicName = name
		}
		// Refresh only the profile fields, atomically. Writing the whole in-memory
		// User back would race with (and undo) a concurrent role edit, and the
		// roles are owned by the admin paths, not by a login.
		if err := dbUpdateUsersList(ctx, func(users []User) []User {
			for i := range users {
				if users[i].Email == email {
					users[i].ID = user.ID
					users[i].Username = user.Username
					users[i].Email = user.Email
					users[i].PublicName = user.PublicName
				}
			}
			return users
		}); err != nil {
			return nil, err
		}
		// Update the live map the same way: merge profile fields onto whatever
		// entry is CURRENT, never store the pre-refresh snapshot wholesale — a
		// role rebuild landing between the Load above and here would otherwise be
		// overwritten with pre-revocation roles (or a deleted entry resurrected).
		if cur, ok := privilegesUsers.Load(email); ok {
			c := cur.(User)
			c.ID, c.Username, c.Email, c.PublicName = user.ID, user.Username, user.Email, user.PublicName
			privilegesUsers.Store(email, c)
		}
	} else {
		user = User{
			ID:       id,
			Username: name,
			Email:    email,
		}
	}

	return &user, nil
}

func registeringEmail(slug, email string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if slug == "" {
		rdb.SAdd(ctx, "registered_emails", email)
	} else {
		rdb.SAdd(ctx, "channel:"+slug+":registered_emails", email)
	}
}
