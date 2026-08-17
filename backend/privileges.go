package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type GlobalRole string
type ChannelRole string

const (
	RoleSuperAdmin GlobalRole = "super_admin"
)

const (
	RoleOwner     ChannelRole = "owner"
	RoleModerator ChannelRole = "moderator"
	RoleWriter    ChannelRole = "writer"
)

var channelRoleLevels = map[ChannelRole]int{
	RoleWriter:    1,
	RoleModerator: 2,
	RoleOwner:     3,
}

var privilegesUsers sync.Map

// initializePrivilegeUsers rebuilds the in-memory privilegesUsers map from Redis.
// At startup, call it directly (panicking is acceptable). From request handlers
// use the returned error instead of panicking so a transient Redis error does not
// crash the entire server.
func initializePrivilegeUsers() error {
	superAdminEmails := strings.Split(os.Getenv("ADMIN_USERS"), ",")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The super-admin stamping is a read-modify-write of the same blob every role
	// edit touches, so it goes through the guarded update too.
	var merged []User
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		emailToIdx := make(map[string]int)
		for i, u := range users {
			emailToIdx[u.Email] = i
		}

		for _, email := range superAdminEmails {
			email = strings.TrimSpace(email)
			if email == "" {
				continue
			}
			if i, exists := emailToIdx[email]; exists {
				users[i].GlobalRole = RoleSuperAdmin
			} else {
				emailToIdx[email] = len(users)
				users = append(users, User{
					Email:      email,
					GlobalRole: RoleSuperAdmin,
				})
			}
		}

		merged = users
		return users
	}); err != nil {
		return fmt.Errorf("update users list: %w", err)
	}

	// Authorization resolves live from this map, so it must never be empty even
	// for an instant: clearing it first would make every request that landed in
	// the gap look unprivileged. Upsert everything, then prune what is gone.
	seen := make(map[string]struct{}, len(merged))
	for _, user := range merged {
		privilegesUsers.Store(user.Email, user)
		seen[user.Email] = struct{}{}
	}
	privilegesUsers.Range(func(key, _ any) bool {
		email, ok := key.(string)
		if !ok {
			privilegesUsers.Delete(key)
			return true
		}
		if _, kept := seen[email]; !kept {
			privilegesUsers.Delete(key)
		}
		return true
	})
	return nil
}

// sessionUser resolves the privileged user record for the current session.
// The session cookie is used only as an identity (email) carrier — the roles
// themselves are read live from privilegesUsers so that a revocation takes
// effect immediately instead of after the 30 day cookie lifetime.
// A logged-in user with no entry in privilegesUsers is simply unprivileged.
func sessionUser(r *http.Request) (User, bool) {
	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok || s.Email == "" {
		return User{}, false
	}
	v, ok := privilegesUsers.Load(s.Email)
	if !ok {
		return User{}, false
	}
	u, ok := v.(User)
	if !ok {
		return User{}, false
	}
	return u, true
}

func isSuperAdmin(r *http.Request) bool {
	u, ok := sessionUser(r)
	return ok && u.GlobalRole == RoleSuperAdmin
}

func hasChannelRole(r *http.Request, slug string, minRole ChannelRole) bool {
	if isSuperAdmin(r) {
		return true
	}
	u, ok := sessionUser(r)
	if !ok || u.ChannelRoles == nil {
		return false
	}
	role, exists := u.ChannelRoles[slug]
	if !exists {
		return false
	}
	return channelRoleLevels[role] >= channelRoleLevels[minRole]
}

func requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSuperAdmin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protectedWithChannelRole(minRole ChannelRole, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := channelSlugFromCtx(r)
		if !hasChannelRole(r, slug, minRole) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		handler(w, r)
	}
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

	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after setPrivilegeUsers: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
