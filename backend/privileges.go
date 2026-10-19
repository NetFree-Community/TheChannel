package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
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

// validChannelRole reports whether r is one of the three channel roles.
func validChannelRole(r ChannelRole) bool {
	switch r {
	case RoleOwner, RoleModerator, RoleWriter:
		return true
	}
	return false
}

var channelRoleLevels = map[ChannelRole]int{
	RoleWriter:    1,
	RoleModerator: 2,
	RoleOwner:     3,
}

var privilegesUsers sync.Map

// privilegesMu serialises every read-then-swap of privilegesUsers, held from
// the users:list read (or transaction) through the swap. Guarding only the
// swap was not enough: rebuild A could load an older users:list, lose the
// race to rebuild B, and still swap its stale snapshot in last, pruning a
// role B had just granted until the next periodic reload.
var privilegesMu sync.Mutex

const (
	// privilegesReloadChannel is the Redis pub/sub channel a role change is
	// announced on, so every replica reloads at once instead of serving the
	// old roles until its next periodic refresh.
	privilegesReloadChannel = "privileges:reload"
	// privilegesRefreshInterval bounds how stale a replica that missed the
	// announcement (or was not subscribed yet) can be.
	privilegesRefreshInterval = 30 * time.Second
)

// normEmail is the canonical form every principal comparison uses. Google
// reports the address in whatever case the account was created with, while
// the super admin and channel owners type it by hand: "Name@Gmail.com" in
// ADMIN_USERS or in a role grant never matched "name@gmail.com" from the
// id_token, so the grant silently did nothing.
func normEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeUsers lower-cases every email and merges the duplicate records that
// case-variant grants left behind, keeping the strongest role per channel and
// the super-admin flag if either copy had it.
func normalizeUsers(users []User) []User {
	out := make([]User, 0, len(users))
	idx := make(map[string]int, len(users))
	for _, u := range users {
		email := normEmail(u.Email)
		if email == "" {
			continue
		}
		u.Email = email
		i, dup := idx[email]
		if !dup {
			idx[email] = len(out)
			out = append(out, u)
			continue
		}
		m := &out[i]
		if m.ID == "" {
			m.ID = u.ID
		}
		if m.Username == "" {
			m.Username = u.Username
		}
		if m.PublicName == "" {
			m.PublicName = u.PublicName
		}
		if u.GlobalRole == RoleSuperAdmin {
			m.GlobalRole = RoleSuperAdmin
		}
		for slug, role := range u.ChannelRoles {
			if m.ChannelRoles == nil {
				m.ChannelRoles = make(map[string]ChannelRole)
			}
			if channelRoleLevels[role] > channelRoleLevels[m.ChannelRoles[slug]] {
				m.ChannelRoles[slug] = role
			}
		}
	}
	return out
}

// superAdminEmails is the ADMIN_USERS list, normalised and sorted so the
// stored users list comes out the same on every rebuild.
func superAdminEmails() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, e := range strings.Split(os.Getenv("ADMIN_USERS"), ",") {
		e = normEmail(e)
		if e == "" {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// initializePrivilegeUsers rebuilds the in-memory privilegesUsers map from Redis.
// At startup, call it directly (panicking is acceptable). From request handlers
// use the returned error instead of panicking so a transient Redis error does not
// crash the entire server.
func initializePrivilegeUsers() error {
	admins := superAdminEmails()
	isAdmin := make(map[string]struct{}, len(admins))
	for _, e := range admins {
		isAdmin[e] = struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Held across the transaction and the swap so that the snapshot swapped in
	// is always the newest one written (see privilegesMu). Nothing below takes
	// the lock: dbUpdateUsersList and notifyPrivilegesChanged only talk to
	// Redis, and the subscriber's reload runs on its own goroutine after this
	// unlocks. The cost is that concurrent role edits are serialised, each
	// bounded by the 5 s context.
	privilegesMu.Lock()
	defer privilegesMu.Unlock()

	// The super-admin stamping is a read-modify-write of the same blob every role
	// edit touches, so it goes through the guarded update too.
	var merged []User
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		users = normalizeUsers(users)
		emailToIdx := make(map[string]int, len(users))
		for i, u := range users {
			emailToIdx[u.Email] = i
			// ADMIN_USERS is the source of truth in both directions. The stamp
			// below only ever added the role, so an address removed from the
			// variable kept super-admin for as long as the users list existed.
			// One boot with the variable unset or mistyped would otherwise
			// persist the demotion of every super admin; an empty list is
			// far more likely a deploy mistake than a real "nobody".
			if u.GlobalRole == RoleSuperAdmin && len(admins) > 0 {
				if _, still := isAdmin[u.Email]; !still {
					users[i].GlobalRole = ""
				}
			}
		}

		for _, email := range admins {
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

	swapPrivilegeMapLocked(merged)
	notifyPrivilegesChanged()
	return nil
}

// swapPrivilegeMapLocked makes users the live authorization set. The caller
// holds privilegesMu, and has held it since it read users from Redis.
//
// Authorization resolves live from this map, so it must never be empty even
// for an instant: clearing it first would make every request that landed in
// the gap look unprivileged. Upsert everything, then prune what is gone.
func swapPrivilegeMapLocked(users []User) {
	seen := make(map[string]struct{}, len(users))
	for _, user := range users {
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
}

// reloadPrivilegeUsers refreshes the live map from Redis without writing
// anything back. This is what the other replicas run when one of them changed
// the roles: before it, a role granted or revoked on one instance took effect
// on the others only after a restart, since each instance only rebuilt its
// own map from its own handlers.
func reloadPrivilegeUsers() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Read and swap under one hold of the lock, or a reload that read before a
	// rebuild wrote could swap its older snapshot in after the rebuild's.
	privilegesMu.Lock()
	defer privilegesMu.Unlock()
	users, err := dbGetUsersList(ctx)
	if err != nil {
		return err
	}
	swapPrivilegeMapLocked(normalizeUsers(users))
	return nil
}

// notifyPrivilegesChanged tells every replica to reload. Best effort: the
// periodic refresh covers a lost announcement.
func notifyPrivilegesChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Publish(ctx, privilegesReloadChannel, "reload").Err(); err != nil {
		log.Printf("privileges: announce reload: %v\n", err)
	}
}

// startPrivilegesRefresh keeps this instance's map in step with the others:
// a subscription for immediate reloads plus a periodic one as the backstop.
func startPrivilegesRefresh() {
	go func() {
		ticker := time.NewTicker(privilegesRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := reloadPrivilegeUsers(); err != nil {
				log.Printf("privileges: periodic reload: %v\n", err)
			}
		}
	}()
	go func() {
		for {
			sub := rdb.Subscribe(context.Background(), privilegesReloadChannel)
			for range sub.Channel() {
				if err := reloadPrivilegeUsers(); err != nil {
					log.Printf("privileges: reload on announcement: %v\n", err)
				}
			}
			// go-redis reconnects a subscription by itself; the channel only
			// closes when the subscription is torn down, so start a new one.
			sub.Close()
			time.Sleep(5 * time.Second)
		}
	}()
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
	v, ok := privilegesUsers.Load(normEmail(s.Email))
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
	s, ok := sessionEmail(r)
	if !ok {
		return false
	}
	return emailHasChannelRole(normEmail(s.Email), slug, minRole)
}

// emailHasChannelRole is hasChannelRole without the session lookup: the live
// record for email (already normalised) is a super admin or holds at least
// minRole on slug. A long-lived handler (the SSE stream) resolves the session
// once and calls this per event, so a revocation reaches it without a
// reconnect; a Load on the sync.Map is all it costs.
func emailHasChannelRole(email, slug string, minRole ChannelRole) bool {
	if email == "" {
		return false
	}
	v, ok := privilegesUsers.Load(email)
	if !ok {
		return false
	}
	u, ok := v.(User)
	if !ok {
		return false
	}
	if u.GlobalRole == RoleSuperAdmin {
		return true
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

	// Upsert-by-email inside the guarded transaction rather than a wholesale
	// replace: the submitted list is a client-side snapshot, and blindly SETting
	// it would silently revert any role granted concurrently through the other
	// (WATCH-guarded) writers. Users absent from the submission are left alone;
	// a future deletion feature needs an explicit deleted-emails field, not
	// inference from absence. The super-admin flag is owned by ADMIN_USERS
	// (see initializePrivilegeUsers) and is kept as stored, whatever the
	// submission says.
	if err := dbUpdateUsersList(ctx, func(current []User) []User {
		current = normalizeUsers(current)
		byEmail := make(map[string]int, len(current))
		for i, u := range current {
			byEmail[u.Email] = i
		}
		for _, nu := range req.List {
			nu.Email = normEmail(nu.Email)
			if nu.Email == "" {
				continue
			}
			if i, ok := byEmail[nu.Email]; ok {
				nu.GlobalRole = current[i].GlobalRole
				current[i] = nu
			} else {
				nu.GlobalRole = ""
				byEmail[nu.Email] = len(current)
				current = append(current, nu)
			}
		}
		return current
	}); err != nil {
		http.Error(w, "Failed to set users list", http.StatusInternalServerError)
		return
	}

	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after setPrivilegeUsers: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
