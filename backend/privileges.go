package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Privilege string              //privilege type
type Privileges map[Privilege]bool // map of privileges
var privilegesUsers sync.Map
var adminUsers []string = strings.Split(os.Getenv("ADMIN_USERS"), ",")

const (
	Admin     Privilege = "admin"     // root privilege
	Moderator Privilege = "moderator" // admin privilege
	Writer    Privilege = "writer"    // can write posts
)

func initializePrivilegeUsers() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	privilegesUsers.Clear()
	users, err := dbGetUsersList(ctx)
	if err != nil && err != redis.Nil {
		panic("Failed to get users list from database: " + err.Error())
	}
	existsEmails := make(map[string]bool)
	for _, user := range users {
		existsEmails[user.Email] = true
	}
	for _, admin := range adminUsers {
		if !existsEmails[admin] {
			users = append(users, User{
				Username: "",
				Email:    admin,
				Blocked:  false,
				Privileges: Privileges{
					Admin:     true,
					Moderator: true,
					Writer:    true,
				},
			})
		} else {
			for i, user := range users {
				if user.Email == admin {
					users[i].Blocked = false
					if users[i].Privileges == nil {
						users[i].Privileges = make(Privileges)
					}
					users[i].Privileges[Admin] = true
					users[i].Privileges[Moderator] = true
					users[i].Privileges[Writer] = true
					break
				}
			}
		}
	}

	for _, user := range users {
		privilegesUsers.Store(user.Email, user)
	}

	if err := dbSetUsersList(ctx, users); err != nil {
		panic("Failed to set users list in database: " + err.Error())
	}
}

func (p Privileges) MarshalBinary() ([]byte, error) {
	return json.Marshal(p)
}

func (p *Privileges) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, p)
}

func getPrivilegeUsersList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func setPrivilegeUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		List []User `json:"list"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	log.Println("Setting privileges for users:", req.List)

	if err := dbSetUsersList(ctx, req.List); err != nil {
		http.Error(w, "Failed to set users list", http.StatusInternalServerError)
		return
	}

	initializePrivilegeUsers()

	response := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
