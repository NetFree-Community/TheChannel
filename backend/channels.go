package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

type ChannelFeatures struct {
	Reactions           bool `json:"reactions"`
	FileUploads         bool `json:"fileUploads"`
	Reports             bool `json:"reports"`
	Ads                 bool `json:"ads"`
	Notifications       bool `json:"notifications"`
	RequireAuth         bool `json:"requireAuth"`
	RequireAuthFiles    bool `json:"requireAuthFiles"`
	CountViews          bool `json:"countViews"`
	ScheduledMessages   bool `json:"scheduledMessages"`
	Webhook             bool `json:"webhook"`
	MagnetLockedByAdmin bool `json:"magnetLockedByAdmin"` // set by super admin, owner cannot change
	AdsLockedByAdmin    bool `json:"adsLockedByAdmin"`    // set by super admin, owner cannot change
	// Disabled is the super admin's kill switch for a whole tenant: set, every
	// route under /api/channel/{slug} is refused, owner included. Stored
	// features blobs written before this field existed simply unmarshal it to
	// false, so no channel is disabled by the upgrade.
	Disabled bool `json:"disabled"`
}

type ChannelData struct {
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	LogoUrl     string          `json:"logoUrl"`
	OwnerEmail  string          `json:"ownerEmail"`
	CreatedAt   time.Time       `json:"createdAt"`
	Features    ChannelFeatures `json:"features"`
	ContactUs   string          `json:"contactUs"`
}

type ctxKey string

const channelCtxKey ctxKey = "channel"

var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,48}[a-z0-9]$`)

// isPlausibleSlug is the lookup-side check (see channelMiddleware); slugRegex
// is the creation-side one.
func isPlausibleSlug(s string) bool {
	return s != "" && len(s) <= 64 && !strings.ContainsAny(s, ":/\\ \t\r\n")
}

// defaultChannelFeatures is the single source of the toggles every new channel
// starts with. Every creation path (createChannel, createChannelSelfService) must
// use it: requireFeature is only applied to toggles both paths default to true,
// and a drifted copy of this literal is exactly what backfillChannelFeatures
// had to repair.
func defaultChannelFeatures() ChannelFeatures {
	return ChannelFeatures{
		Reactions:         true,
		FileUploads:       true,
		Reports:           true,
		ScheduledMessages: true,
		// Permission to use the feature, not a request to use it: each one
		// still needs the owner to configure it in channel settings. Left
		// false these would be unusable and undiagnosable for a new tenant.
		Ads:           true,
		Notifications: true,
		Webhook:       true,
	}
}

func channelMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		ctx := r.Context()

		// Refuse what can never be a channel before touching Redis. Keys are
		// "channel:<slug>:<suffix>", so a slug carrying ':' (e.g.
		// "x:messages:5") resolved the message hash itself as a phantom
		// channel with every feature off. The check is deliberately looser
		// than the creation regex so channels created before that regex
		// existed stay reachable.
		if !isPlausibleSlug(slug) {
			http.Error(w, "Channel not found", http.StatusNotFound)
			return
		}

		channel, err := dbGetChannel(ctx, slug)
		if err != nil {
			if err == redis.Nil {
				http.Error(w, "Channel not found", http.StatusNotFound)
			} else {
				http.Error(w, "error", http.StatusInternalServerError)
			}
			return
		}

		// A disabled channel is refused here, before anything channel-scoped
		// runs, so the block covers reads, writes and the owner's own admin
		// routes alike. Super-admin routes live outside this router group, so a
		// disabled channel can still be re-enabled.
		if channel.Features.Disabled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "channel_disabled"})
			return
		}

		ctx = context.WithValue(ctx, channelCtxKey, channel)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func channelFromCtx(r *http.Request) *ChannelData {
	v := r.Context().Value(channelCtxKey)
	if v == nil {
		return nil
	}
	return v.(*ChannelData)
}

func channelSlugFromCtx(r *http.Request) string {
	ch := channelFromCtx(r)
	if ch == nil {
		return ""
	}
	return ch.Slug
}

// requireFeature rejects a request when the channel's operator-controlled
// toggle for that feature is off. Only applied to toggles both channel
// creation paths default to true, so it cannot silently disable a live tenant.
func requireFeature(pick func(*ChannelFeatures) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch == nil || !pick(&ch.Features) {
			http.Error(w, "Feature disabled for this channel", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// channelIfRequireAuth middleware
func channelIfRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch != nil && ch.Features.RequireAuth {
			checkLogin(next).ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// channelIfRequireAuthFiles middleware
func channelIfRequireAuthFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch := channelFromCtx(r)
		if ch != nil && ch.Features.RequireAuthFiles {
			checkLogin(next).ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// Super admin: list all channels
func listChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channels)
}

// Super admin: create channel
func createChannel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		Slug       string `json:"slug"`
		Name       string `json:"name"`
		OwnerEmail string `json:"ownerEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if !slugRegex.MatchString(req.Slug) {
		http.Error(w, "Invalid slug: use lowercase letters, numbers, hyphens (min 3 chars)", http.StatusBadRequest)
		return
	}
	// The self-service path and the info editor both refuse a blank name; this
	// path did not, and a nameless channel renders with an empty header and a
	// "?" avatar. A mistyped owner email is worse: the role is granted to an
	// identity nobody can sign in as, and the channel has no reachable owner.
	req.Name = strings.TrimSpace(req.Name)
	req.OwnerEmail = normEmail(req.OwnerEmail)
	if req.Name == "" {
		http.Error(w, "Channel name is required", http.StatusBadRequest)
		return
	}
	if channelFieldTooLong(req.Name, maxChannelNameLen) {
		http.Error(w, "Channel name is too long", http.StatusBadRequest)
		return
	}
	if req.OwnerEmail != "" && !looksLikeEmail(req.OwnerEmail) {
		http.Error(w, "Invalid owner email", http.StatusBadRequest)
		return
	}
	// The reserved check lived only in the self-service path; the admin path
	// must refuse the same slugs (see reservedSlugs for why they are kept).
	if isReservedSlug(req.Slug) {
		http.Error(w, "Slug is reserved", http.StatusBadRequest)
		return
	}

	exists, err := dbChannelExists(ctx, req.Slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "Channel already exists", http.StatusConflict)
		return
	}

	channel := &ChannelData{
		Slug:       req.Slug,
		Name:       req.Name,
		OwnerEmail: req.OwnerEmail,
		CreatedAt:  time.Now(),
		Features:   defaultChannelFeatures(),
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		if errors.Is(err, errChannelExists) {
			http.Error(w, "Channel already exists", http.StatusConflict)
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if req.OwnerEmail != "" {
		if err := dbAssignChannelRole(ctx, req.OwnerEmail, req.Slug, RoleOwner); err != nil {
			log.Printf("createChannel: %s created but owner role for %s not assigned: %v\n", req.Slug, req.OwnerEmail, err)
		}
		if err := initializePrivilegeUsers(); err != nil {
			log.Printf("initializePrivilegeUsers after createChannel: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// Super admin: delete channel
func deleteChannel(w http.ResponseWriter, r *http.Request) {
	// Deletion also releases every uploaded blob (R2/disk), so it needs more
	// headroom than the usual 5s handler budget.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")

	// Without these checks a typo deletes nothing and still reports success, so
	// the operator records a channel as gone while it is still live. The
	// plausibility check, not the creation regex: a channel created before
	// the regex existed (uppercase, underscore, two characters) could be
	// viewed and edited but never deleted.
	if !isPlausibleSlug(slug) {
		http.Error(w, "Invalid slug", http.StatusBadRequest)
		return
	}
	exists, err := dbChannelExists(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	// Hold the scheduled-dispatch claim for the whole delete (see
	// claimScheduled); the TTL outlives this handler's budget.
	// The dispatcher refreshes its claim per posted message and can hold it
	// for most of a minute on a channel with many due posts; two seconds of
	// waiting turned that into a 503 the operator had to retry by hand.
	token, ok := claimScheduled(ctx, slug, 90*time.Second, 15*time.Second)
	if !ok {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "channel is busy, try again", http.StatusServiceUnavailable)
		return
	}
	defer releaseScheduledLock(slug, token)

	// Viewers with an open event stream never learned the channel was gone:
	// the stream key vanished underneath the hub, which kept polling it, and
	// the heartbeat kept the page looking live until the next reload. Tell
	// them first, and give the hub a moment to fan the event out before the
	// stream is deleted.
	publishEvent(ctx, slug, []byte(sseChannelGoneEvent))
	time.Sleep(500 * time.Millisecond)
	// Then retire this instance's hub outright: a client that ignores the
	// event, or a stale connection, would otherwise keep its stream — and
	// the writer resolution it was opened with — for as long as it stayed up.
	streamKey := "channel:" + slug + ":events"
	sseStopHub(streamKey)

	if err := dbDeleteChannel(ctx, slug); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// A reconnect that slipped in between the stop above and the fence
	// inside dbDeleteChannel built a fresh hub on a key that no longer
	// exists, where it would sit and pick up the events of whatever tenant
	// re-creates the slug. Now that the channel is gone, reconnects meet 404.
	sseStopHub(streamKey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// Super admin: update channel features
func updateChannelFeatures(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	if !requireExistingChannel(ctx, w, slug) {
		return
	}

	var features ChannelFeatures
	if err := json.NewDecoder(r.Body).Decode(&features); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// The two *LockedByAdmin mirrors are owned by the global ads/magnet lock
	// lists and their sync, never by this form: it loads them and passes them
	// back untouched, so a save made after the global lock changed used to
	// replay the stale pair. They are kept from the stored record, and the
	// write is atomic against the sync so neither side reverts the other.
	var wasDisabled bool
	err := dbUpdateChannelFeatures(ctx, slug, func(cur *ChannelFeatures) {
		wasDisabled = cur.Disabled
		magnetLocked, adsLocked := cur.MagnetLockedByAdmin, cur.AdsLockedByAdmin
		*cur = features
		cur.MagnetLockedByAdmin, cur.AdsLockedByAdmin = magnetLocked, adsLocked
	})
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// The kill switch used to stop new requests only: every open event
	// stream — a since-demoted writer's included — kept streaming, unmasked,
	// until its connection dropped. The control event tells the clients and
	// every hub, this instance's included, which retire themselves after
	// fanning it out. Reconnects meet channelMiddleware's 403.
	if features.Disabled && !wasDisabled {
		announceChannelDisabled(ctx, slug)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// announceChannelDisabled publishes the channel-disabled control event and
// retires this instance's hub once the event had a chance to fan out.
//
// The hub used to be stopped right after the publish. sseStopHub closes every
// subscriber at once, and the hub's blocking read returned with the event only
// afterwards, straight into its "already cancelled" exit — so on the instance
// that served the request (every viewer, in a single-replica deployment) the
// viewers lost the very event that tells them the channel is disabled and kept
// a live-looking page until the heartbeat watchdog reconnected them into a
// 403. The hub retires itself when it reads the control event (see run); the
// delayed stop only covers a hub that never read it, and does not hold the
// request. A hub built for the slug within the delay (a re-enable in that
// window) is stopped too, which costs its viewers one reconnect.
func announceChannelDisabled(ctx context.Context, slug string) {
	publishEvent(ctx, slug, []byte(sseChannelDisabledEvent))
	streamKey := "channel:" + slug + ":events"
	time.AfterFunc(500*time.Millisecond, func() { sseStopHub(streamKey) })
}

// requireExistingChannel answers 404 and returns false when slug does not name
// a channel. The super-admin write handlers take the slug straight from the
// URL, outside channelMiddleware, and used to write a features blob or a role
// grant for a slug that did not exist — silently, with a success response.
func requireExistingChannel(ctx context.Context, w http.ResponseWriter, slug string) bool {
	if !isPlausibleSlug(slug) {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return false
	}
	exists, err := dbChannelExists(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return false
	}
	if !exists {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return false
	}
	return true
}

// Super admin: get channel (including features)
func getSuperAdminChannel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	channel, err := dbGetChannel(ctx, slug)
	if err != nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// channelUserChange is one requested role change on a channel's user list.
type channelUserChange struct {
	Email string      `json:"email"`
	Role  ChannelRole `json:"role"`
}

// applyChannelRoleChanges returns the dbUpdateUsersList callback shared by the
// super-admin and owner user-management handlers. allowOwner is the only
// difference between them: a channel owner may not promote others to owner.
func applyChannelRoleChanges(slug string, changes []channelUserChange, allowOwner bool) func([]User) []User {
	return func(users []User) []User {
		users = normalizeUsers(users)
		userMap := make(map[string]int)
		for i, u := range users {
			userMap[u.Email] = i
		}

		for _, ru := range changes {
			ru.Email = normEmail(ru.Email)
			if ru.Email == "" {
				continue
			}
			if !allowOwner && ru.Role == RoleOwner {
				continue // owner cannot promote others to owner
			}
			if i, exists := userMap[ru.Email]; exists {
				// Guarding only the promotion above left the reverse open: a
				// change to "" or to any lesser role is not RoleOwner, so an
				// owner could strip a co-owner's role and take sole control of
				// the tenant — irreversibly, since only a super admin can grant
				// owner back. Who currently holds owner is what matters here,
				// not what the change asks for.
				if !allowOwner && users[i].ChannelRoles[slug] == RoleOwner {
					continue
				}
				if users[i].ChannelRoles == nil {
					users[i].ChannelRoles = make(map[string]ChannelRole)
				}
				if ru.Role == "" {
					delete(users[i].ChannelRoles, slug)
				} else {
					users[i].ChannelRoles[slug] = ru.Role
				}
			} else if ru.Role != "" {
				userMap[ru.Email] = len(users)
				users = append(users, User{
					Email:        ru.Email,
					ChannelRoles: map[string]ChannelRole{slug: ru.Role},
				})
			}
		}
		return users
	}
}

// setChannelUsersForSlug decodes the request and applies the role changes under
// the guarded users:list transaction. Shared by both set-users handlers.
func setChannelUsersForSlug(w http.ResponseWriter, r *http.Request, slug string, allowOwner bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		Users []channelUserChange `json:"users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// A role is one of three words; anything else was stored as typed,
	// echoed back by the users list as if granted, and granted nothing.
	for _, u := range req.Users {
		if u.Role != "" && !validChannelRole(u.Role) {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		// A grant to something that is not an address ("yossi.gmail.com")
		// was saved with a success toast and could never match a login: the
		// intended person stayed locked out and a phantom user record was
		// created. Revocations are left alone so a bad entry can be removed.
		if u.Role != "" && !looksLikeEmail(normEmail(u.Email)) {
			http.Error(w, "invalid email: "+strings.TrimSpace(u.Email), http.StatusBadRequest)
			return
		}
	}

	// Read-modify-write under a WATCH so a concurrent role edit elsewhere is not
	// silently overwritten by this one. The mutator may run more than once;
	// the last run is the one that committed, so its output is what is kept.
	var updated []User
	mutate := applyChannelRoleChanges(slug, req.Users, allowOwner)
	if err := dbUpdateUsersList(ctx, func(users []User) []User {
		updated = mutate(users)
		return updated
	}); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// The channel hash's ownerEmail (what the channels list shows as owner)
	// was written at creation only, so moving owner through this editor left
	// the list naming the old one. Owner can only change when allowOwner.
	if allowOwner {
		owner := ""
		for _, u := range updated {
			if u.ChannelRoles[slug] == RoleOwner {
				owner = u.Email
				break
			}
		}
		// Written only if the hash still exists: a bare HSET would otherwise
		// mint a phantom channel for a slug deleted a moment ago.
		if err := rdb.Eval(ctx, `if redis.call('EXISTS', KEYS[1]) == 1 then return redis.call('HSET', KEYS[1], 'ownerEmail', ARGV[1]) end return 0`,
			[]string{"channel:" + slug}, owner).Err(); err != nil {
			log.Printf("setChannelUsers(%s): ownerEmail not updated: %v", slug, err)
		}
	}
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after setChannelUsers(%s): %v", slug, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// listChannelUsers writes the channel's user/role list. Shared by the
// super-admin and owner get-users handlers.
//
// hideOperators drops an operator's owner row. An operator holds owner on every
// channel they created themselves, and a co-owner reading that list must not
// learn the operator's email. Only owner rows are hidden: an owner can never
// grant owner, so such a row was never their own doing, whereas a writer or
// moderator row on an operator's email is one an owner typed in and must stay
// visible — hiding it too would make the row vanish after saving, which both
// looks like a failed save and confirms that the address is the operator's.
// Hiding changes nothing about access: a super admin reaches every channel
// through hasChannelRole anyway, and an owner's save only touches the rows it
// sends.
func listChannelUsers(w http.ResponseWriter, slug string, hideOperators bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	type ChannelUser struct {
		Email string      `json:"email"`
		Role  ChannelRole `json:"role"`
	}

	// Encoded straight to JSON: a nil slice would serialise as null, which the
	// user management screens do not accept.
	channelUsers := make([]ChannelUser, 0)
	for _, u := range users {
		if hideOperators && u.GlobalRole == RoleSuperAdmin && u.ChannelRoles[slug] == RoleOwner {
			continue
		}
		if u.ChannelRoles != nil {
			if role, exists := u.ChannelRoles[slug]; exists {
				channelUsers = append(channelUsers, ChannelUser{Email: u.Email, Role: role})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channelUsers)
}

// Super admin: set channel users
func superAdminSetChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	slug := chi.URLParam(r, "slug")
	if !requireExistingChannel(ctx, w, slug) {
		return
	}
	setChannelUsersForSlug(w, r, slug, true)
}

// Super admin: get channel users
func superAdminGetChannelUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	slug := chi.URLParam(r, "slug")
	if !requireExistingChannel(ctx, w, slug) {
		return
	}
	listChannelUsers(w, slug, false)
}

// Channel owner: get channel users (for this channel only)
func getChannelUsers(w http.ResponseWriter, r *http.Request) {
	listChannelUsers(w, channelSlugFromCtx(r), !isSuperAdmin(r))
}

// Channel owner: set channel users (cannot assign owner role)
func setChannelUsers(w http.ResponseWriter, r *http.Request) {
	setChannelUsersForSlug(w, r, channelSlugFromCtx(r), false)
}
