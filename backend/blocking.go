package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func blockUser(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.Email == "" {
		http.Error(w, "Email is required", http.StatusBadRequest)
		return
	}

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	userFound := false
	for i, user := range users {
		if user.Email == req.Email {
			if user.Privileges[Admin] {
				http.Error(w, "Cannot block admin user", http.StatusForbidden)
				return
			}
			users[i].Blocked = true
			userFound = true
			break
		}
	}

	if !userFound {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	if err := dbSetUsersList(ctx, users); err != nil {
		http.Error(w, "Failed to update users list", http.StatusInternalServerError)
		return
	}

	initializePrivilegeUsers()

	response := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func unblockUser(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.Email == "" {
		http.Error(w, "Email is required", http.StatusBadRequest)
		return
	}

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	userFound := false
	for i, user := range users {
		if user.Email == req.Email {
			users[i].Blocked = false
			userFound = true
			break
		}
	}

	if !userFound {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	if err := dbSetUsersList(ctx, users); err != nil {
		http.Error(w, "Failed to update users list", http.StatusInternalServerError)
		return
	}

	initializePrivilegeUsers()

	response := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getBlockedUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	var blockedUsers []User
	for _, user := range users {
		if user.Blocked {
			blockedUsers = append(blockedUsers, user)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(blockedUsers)
}

// temporary
func getAllUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	type UserWithStatus struct {
		User
		IsAdmin bool `json:"isAdmin"`
	}

	var usersWithStatus []UserWithStatus
	for _, user := range users {
		usersWithStatus = append(usersWithStatus, UserWithStatus{
			User:    user,
			IsAdmin: user.Privileges[Admin],
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(usersWithStatus)
}

func getUserBlockStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "Email parameter is required", http.StatusBadRequest)
		return
	}

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	for _, user := range users {
		if user.Email == email {
			response := struct {
				Email   string `json:"email"`
				Blocked bool   `json:"blocked"`
				IsAdmin bool   `json:"isAdmin"`
			}{
				Email:   user.Email,
				Blocked: user.Blocked,
				IsAdmin: user.Privileges[Admin],
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	http.Error(w, "User not found", http.StatusNotFound)
}