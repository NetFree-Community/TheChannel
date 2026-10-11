# TheChannel — Multi-Tenant System — Change Log

## Overview

This document describes all changes made to transform TheChannel from a single-channel application into a **multi-tenant platform** where each user can own a private channel, with a central super-admin panel for full control.

---

## Architecture Changes

### Multi-Tenant Backend (Go)

All data is now stored with per-channel Redis key prefixes:

| Old key pattern | New key pattern |
|---|---|
| `messages:{id}` | `channel:{slug}:messages:{id}` |
| `m_times` | `channel:{slug}:m_times` |
| `settings:list` | `channel:{slug}:settings` |
| (none) | `global:settings` (FCM/VAPID) |

A single Go backend instance and a single Redis/Kvrocks instance serve **all channels**. No new CapRover projects needed per channel.

---

## New Files

### Backend

| File | Description |
|---|---|
| `backend/channels.go` | Channel CRUD, features management, middleware |
| `backend/storage.go` | Cloudflare R2 client (`initR2`, `r2Upload`, `r2Download`, `r2Delete`, `r2Exists`) |
| `backend/storage_handlers.go` | Storage quota HTTP handlers for channel admin and super admin |

### Frontend

| File | Description |
|---|---|
| `frontend/src/app/guards/super-admin.guard.ts` | Route guard for `/super-admin` — only allows `globalRole === 'super_admin'` |
| `frontend/src/app/services/super-admin.service.ts` | All super admin API calls |
| `frontend/src/app/components/super-admin/super-admin-panel.component.*` | Main super admin shell |
| `frontend/src/app/components/super-admin/channels/channels-list.component.*` | Channel list with CRUD actions |
| `frontend/src/app/components/super-admin/channels/channel-features.component.*` | Per-channel feature toggles (13 features) |
| `frontend/src/app/components/super-admin/channels/channel-users.component.*` | Per-channel user/role management |
| `frontend/src/app/components/super-admin/global-ads/global-ads.component.*` | Global iframe-ads config + lock |
| `frontend/src/app/components/super-admin/global-magnet/global-magnet.component.*` | Global Magnet ads config + frequency + lock |
| `frontend/src/app/components/super-admin/global-users/global-users.component.*` | Read-only global user list with roles |
| `frontend/src/app/components/super-admin/global-settings/global-settings.component.*` | Global key-value settings editor (FCM/VAPID) |
| `frontend/src/app/components/super-admin/statistics/super-admin-statistics.component.*` | Magnet stats + statistics reset |
| `frontend/src/app/components/super-admin/storage/super-admin-storage.component.ts` | Per-channel storage quota + usage bar (super admin view) |
| `frontend/src/app/components/super-admin/global-storage/global-storage.component.ts` | Global default storage quota editor |
| `frontend/src/app/components/admin/storage/storage.component.ts` | Channel admin storage view — usage bar, warnings, auto-cleanup toggle |

---

## Modified Files

### Backend

#### `backend/privileges.go` — Complete rewrite
- New role system: `GlobalRole` (`super_admin`) and `ChannelRole` (`owner`, `moderator`, `writer`)
- `User` struct now holds `GlobalRole` and `ChannelRoles map[string]ChannelRole`
- Middleware: `requireSuperAdmin`, `protectedWithChannelRole`

#### `backend/auth.go`
- `Session` struct updated with `GlobalRole` and `ChannelRoles`
- `registeringEmail(slug, email string)` — `slug=""` registers globally, slug set registers into a channel

#### `backend/db.go` — Complete rewrite
- All message/settings functions are now channel-scoped (slug prefix)
- Channel CRUD: `dbCreateChannel`, `dbGetChannel`, `dbListChannels`, `dbDeleteChannel`, `dbSetChannelFeatures`, `dbAssignChannelRole`
- Storage quota functions:
  - `dbGetGlobalStorageQuota` / `dbSetGlobalStorageQuota`
  - `dbGetChannelStorageQuota` / `dbSetChannelStorageQuota` (0 = use global)
  - `dbGetEffectiveStorageQuota` (returns channel-specific or global default)
  - `dbGetChannelStorageUsed` / `dbIncrChannelStorageUsed` / `dbDecrChannelStorageUsed`
  - `dbAddChannelFile` / `dbRemoveChannelFile` (sorted set by upload timestamp)
  - `dbGetOldestChannelFiles` (for auto-cleanup)
  - `dbGetChannelAutoCleanup` / `dbSetChannelAutoCleanup`
  - `dbIncrFileHashRefs` / `dbDecrFileHashRefs` (deduplication reference counter)
- Global ads/magnet config storage: `dbGetGlobalMagnetConfig`, `dbSetGlobalMagnetConfig`, `dbGetGlobalAdsConfig`, `dbSetGlobalAdsConfig`

#### `backend/files.go`
- `FileMetadata` struct: added `Size int64` and `ChannelSlug string`
- `dbSaveFileMetadata` / `dbGetFileMetadata` — now uses Redis JSON; YAML fallback for legacy local files
- `uploadFile` — reads bytes to memory, checks quota, deduplicates by SHA-256 hash, increments ref counter, tracks in channel sorted set
- `reserveStorageQuota` — checks quota before upload; runs auto-cleanup (target: 80% usage) if enabled
- `deleteFileByID` — marks deleted, decrements storage counter, removes from R2/disk only when refs reach 0
- **TinyPNG integration**: `compressWithTinyPng(ctx, apiKey, data, mimeType)` — compresses PNG/JPEG/WebP via TinyPNG API before upload if `tinypng_api_key` is set in channel settings

#### `backend/settings.go`
- `SettingConfig` struct: added `TinyPngApiKey string`
- `ToConfig()`: added `"tinypng_api_key"` case

#### `backend/ads.go`
- `isChannelMagnetLocked` / `isChannelAdsLocked` — checks if super admin locked ads for this channel
- `getMagnetAdsSettings` / `getAdsSettings` — returns global config if locked, channel config otherwise
- `getGlobalMagnetConfig` / `setGlobalMagnetConfig` — super admin global Magnet config
- `getGlobalAdsConfig` / `setGlobalAdsConfig` — super admin global iframe-ads config
- `syncMagnetLockFlags` / `syncAdsLockFlags` — goroutines that update `MagnetLockedByAdmin`/`AdsLockedByAdmin` on all channels when global config changes

#### `backend/main.go` — Full routing rewrite

```
/auth/google
/auth/login
/auth/logout
/favicon.ico, /assets/favicon.ico
/firebase-messaging-sw.js

(login required, global)
  GET  /api/user-info
  POST /api/channels/create          self-service channel creation
  GET  /api/channels/slug-available

/api/support/*  (no login; an anonymous thread is guarded by its token)
  POST /tickets
  GET  /tickets/{id}
  POST /tickets/{id}/reply
  GET  /my-tickets                   (login required)

/api/super-admin/*  (login + requireSuperAdmin)
  GET  /channels
  POST /channels/create
  GET  /channels/{slug}
  DELETE /channels/{slug}
  PUT  /channels/{slug}/features
  GET  /channels/{slug}/users
  POST /channels/{slug}/users
  GET  /channels/{slug}/storage
  PUT  /channels/{slug}/storage
  GET  /users/list
  POST /users/set
  GET  /global-settings/get
  POST /global-settings/set
  GET  /ads/config
  POST /ads/config
  GET  /magnet/config
  POST /magnet/config
  GET  /magnet/stats
  GET  /storage/config
  POST /storage/config
  POST /statistics/reset
  GET  /support/tickets
  POST /support/tickets/{id}/reply
  POST /support/tickets/{id}/status
  GET  /channel-requests
  POST /channel-requests/{id}/approve
  POST /channel-requests/{id}/reject

/api/channel/{slug}/import/post  (API key auth)

/api/channel/{slug}/*  (channelMiddleware + channelIfRequireAuth)
  GET  /info
  GET  /messages
  GET  /events
  GET  /files/{fileid}
  GET  /emojis/list
  GET  /notifications-config
  GET  /ads/settings
  GET  /ads/magnet
  (login required):
    POST /notifications-subscribe
    POST /reactions/set-reactions
    POST /messages/report
    GET  /user-info

  /admin/*  (channel role: writer < moderator < owner; a higher tier includes the lower)
    writer:
      POST   /new
      POST   /edit-message
      DELETE /delete-message/{id}
      POST   /upload
      GET    /scheduled-messages/get
      POST   /scheduled-messages/update
    moderator:
      POST /edit-channel-info
      GET  /statistics
      POST /set-emojis
      GET  /reports/get
      POST /reports/set
    owner:
      GET  /settings/get
      POST /settings/set
      GET  /users/get
      POST /users/set
      GET  /storage
      POST /storage/auto-cleanup
```

`backend/main.go` is the authoritative route table; the block above mirrors it.

### Frontend

#### `frontend/src/app/models/channel.model.ts`
- Added `slug?: string` field

#### `frontend/src/app/models/user.model.ts`
- Added `globalRole: string` and `channelRoles: Record<string, string>`

#### `frontend/src/app/app.routes.ts`
- Added `/super-admin` route guarded by `AuthGuard` + `SuperAdminGuard`

#### `frontend/src/app/components/admin/admin-panel.component.ts`
- Added `StorageComponent` import
- Added `"אחסון"` menu item with `hard-drive-outline` icon
- Added `readonly storage = "storage"` constant
- Added `channelSlug` getter via `ChatService`
- Added `case 'hard-drive-outline'` in menu switch

#### `frontend/src/app/components/admin/admin-panel.component.html`
- Added `@case (storage)` rendering `<app-channel-storage [slug]="channelSlug">`

#### `frontend/src/app/components/admin/settings/settings.schema.ts`
- Added new category **"אחסון ומדיה"** with `tinypng_api_key` field (password type)

---

## Features Added

### 1. Multi-Tenant Channels
- Each user gets their own private channel with a unique slug
- Single Go + Redis instance serves all channels
- No CapRover project needed per channel

### 2. Role System
- **GlobalRole**: `super_admin`
- **ChannelRole**: `owner` > `moderator` > `writer`
- All API routes protected with appropriate role middleware

### 3. Super Admin Panel (`/super-admin`)
- Accessible only to users with `globalRole: "super_admin"`
- **Channels**: List all channels, create, delete, edit features, manage users, manage storage
- **Iframe Ads**: Global iframe-ad src/width with option to lock all channels or specific channels
- **Magnet Ads**: Global Magnet config (snippet, mode, frequency settings) with lock option
- **Global Storage**: Set the default storage quota (GB) for all channels
- **Users**: Read-only list of all users with their global and channel roles
- **Global Settings**: FCM/VAPID key-value editor
- **Statistics**: View Magnet stats, reset all statistics

### 4. Storage Quota System
- Super admin sets a **global default quota** (default: 5 GB per channel)
- Super admin can **override per-channel** from the channels list → "אחסון"
- Channel owners see **usage bar** with color-coded status:
  - Green (`ok`): below 80% used
  - Orange (`warning`): 80% up to, not including, 90%
  - Red (`critical`): 90% and above
- **Auto-cleanup toggle**: When enabled, an upload that would exceed the quota first deletes the channel's oldest files (at most the 200 oldest, never the current logo), aiming for 80% usage; if that does not free enough space the upload is still refused with `storage quota exceeded`
- File deduplication: identical files (same SHA-256 hash) share one physical copy; storage only freed when last reference is removed

### 5. Cloudflare R2 Storage
- All media uploads go to R2 (S3-compatible)
- Local disk used as fallback when R2 is not configured
- Configure via environment variables: `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET_NAME`, `R2_PUBLIC_URL`
- Files stored at path: `files/{hash[0:2]}/{hash[2:4]}/{hash}`

### 6. TinyPNG Image Compression
- Channel owners add their TinyPNG API key in **Admin → הגדרות → אחסון ומדיה**
- When set, PNG/JPEG/WebP images are automatically compressed via TinyPNG API **before** upload
- Compressed file is what gets stored and counted toward quota — saves significant space
- Falls back silently to original file if compression fails or API is unavailable
- Non-image files (video, documents, etc.) are never sent to TinyPNG

### 7. Global Ads Override
- Super admin can push **global iframe-ads** settings to all channels or specific channels
- Super admin can push **global Magnet ads** settings (including frequency controls) to all channels or specific channels
- Locked channels display the global config and cannot be overridden by channel owners

---

## Environment Variables

Add to your `.env` / CapRover environment:

```
# Cloudflare R2
# Leave all four unset to keep files on local disk; a value starting with
# "your_" is treated as unset. No inline comments: env-file loaders keep them
# as part of the value.
R2_ACCOUNT_ID=
R2_ACCESS_KEY_ID=
R2_SECRET_ACCESS_KEY=
R2_BUCKET_NAME=
# optional, for direct CDN links
R2_PUBLIC_URL=
```

---

## Dependencies Added

### Go (`backend/go.mod`)
- `github.com/aws/aws-sdk-go-v2` — AWS SDK v2 (used for R2 via S3-compatible API)
- `github.com/aws/aws-sdk-go-v2/credentials`
- `github.com/aws/aws-sdk-go-v2/service/s3`

---

## UX overhaul (October 2026)

A visual and structural rework of the whole frontend, plus two backend additions. Behaviour and endpoints are unchanged unless listed here.

### Backend
- `GET /api/my-channels` — every channel the signed-in user holds a role on (name, logo, role, disabled flag, participant count), sorted owners first. Backs the "my channels" page and the channel switcher.
- gzip compression (chi `middleware.Compress`) for HTML, CSS, JavaScript, JSON and SVG. The SSE stream is routed around the middleware entirely (`backend/compress.go`): chi v1's compressing writer has no `SetWriteDeadline`, so wrapped, the 30 s write deadline `getEvents` arms could never be set and a dead viewer kept its connection slot until TCP timed out. Media is excluded by content type.
- Link previews: `serveSpaFile` injects `<title>` and Open Graph tags (`backend/seo.go`). `/channel/<slug>` carries the channel's name, description and logo; every other page the platform card (`/assets/og.jpg`). Private and disabled channels get the generic card and leak nothing. `.webmanifest` is registered as `application/manifest+json`.
- Support tickets: the access token opens anonymous tickets only. A signed-in ticket is reachable by its session alone — tokens issued to signed-in senders before this change (still in the browser's localStorage after logout) no longer open them. Test.

### Frontend
- Brand theme registered through Nebular tokens (`frontend/src/themes.scss`); every screen reads colours, radii and shadows from it. The prebuilt Nebular `default.css` is no longer loaded (it duplicated the compiled theme), Bootstrap is compiled from a subset (`frontend/src/bootstrap-subset.scss`), the Bootstrap Icons CDN stylesheet and the 24 MB NotoColorEmoji font are gone (system emoji font).
- Lazy routes: landing, login, super-admin and the new manage page load on demand; the channel page stays in the initial bundle. Firebase is imported only on channels with push enabled.
- The channel admin panel is now a page: `/channel/:slug/manage/:tab` (ChannelManagerGuard), with a channel switcher. The header's user menu lists the user's other channels for one-tap switching.
- "My channels" hub at `/channel` with names, logos, roles, share buttons and the 5-channel cap explained; one-moment channel creation with a live URL preview and a share-ready success screen.
- Shared primitives: `ShareService` (copy / WhatsApp / OS share), `ConfirmService` + `ConfirmDialogComponent` (replaces `window.confirm` / `alert`), `MyChannelsService`.
- Message times use `Intl` instead of moment.
- Only the brand theme is emitted to CSS (`$nb-enabled-themes: (custom)`): the unused `default` parent block was 211 KB of custom properties in every visitor's stylesheet. Type-scale and motion tokens (`--text-*`, `--motion-*`, `--ease-out`) in `styles.scss`; buttons transition and press.
- Toasts stay 5 s, close on click and do not stack duplicates (`app.config.ts`).
- Brand assets in `frontend/public/`: SVG favicon, apple-touch-icon, 512px icon, web app manifest, Open Graph card.
- Eva icons: only the names in use are registered (`src/app/eva-icons.subset.ts`, generated by `node scripts/gen-eva-subset.mjs`; `--check` fails when stale) instead of `NbEvaIconsModule`'s whole pack. A new icon name needs a re-run of the script, otherwise it renders empty. `scripts/check-bootstrap-classes.mjs <dist/browser>` verifies every Bootstrap-looking template class exists in the compiled CSS.
- Dependencies dropped: `@ng-bootstrap/ng-bootstrap`, `@popperjs/core` (the reaction popover is Nebular's now) and `moment` (replaced by `Intl`).
- Channel open: the route guard fetches `/info` through `ChatService` in parallel with the session check, and `initChannel` keeps that answer (`clearCache(slug)`), so the header no longer requests `/info` a second time and the first `/messages` starts one round trip earlier.
- Landing CTA goes straight to Google (`loginWithGoogle()`); `/login` remains the OAuth callback and the fallback when the hand-off fails.
- Phones: header height 4.25rem (from 4.75rem) via a `--header-height` override in `styles.scss`; the composer toolbar wraps instead of scrolling sideways; the super-admin chip navigation scrolls inside its box instead of widening the page.
- Document titles everywhere, brand name "הערוץ" throughout.
- Nebular's tooltip, popover and context-menu directives each provide the same `NbDynamicOverlayHandler` on their host element, so two of them on one element share a single handler and only the directive built last keeps its trigger. A tooltip beside the header's user menu, share menu and the message ⋮ menu had left those menus dead (only the tooltip showed). The tooltips are gone from those elements; never pair `nbTooltip` with `nbPopover`/`nbContextMenu` on one element.
- Adversarial review fixes: the message ⋮ menu is keyboard-operable (focus moves in, arrows/Home/End/Escape, focus returns); quoting a message while an edit is open prepends to the composer instead of rewriting the entry being edited (a scheduled entry is found on the server by its text, so the save used to fail); editing a scheduled message no longer crashes the time picker on the wire-format timestamp; sending from markdown preview returns to the editor; Back from Google's account picker (bfcache restore) resets the login and landing buttons; the manage page mounts only the sections the role may open; a dead channel link keeps the guard's not-found verdict instead of flashing the chat shell; slug verdict chips, composer strip buttons and the feed banner meet contrast and tap-target sizes; copy fixes (support box named "פנייה לתמיכה" everywhere, channel-limit text, magnet lock text, role hints, SET.md labels).
- Browser sweep fixes (28 findings reproduced with Playwright against a local build, each fixed): switching channels in-app (user menu, Back) now reloads the feed, the SSE stream and the tab title instead of keeping the previous channel's; the header share menu, user menu, about card and the formatting-help popover are keyboard-operable (focus moves in, arrows/Home/End, Enter activates, Escape and Tab close and return focus, aria-expanded on the triggers, menu roles); the header shows the public name; the create form keeps the field, the preview and the verdict in step (illegal characters never show, a cleared address falls back to the name, Enter waits for the live address check), and ?new=1 follows what is shown; a not-found/disabled card never mounts the chat shell or fires requests for the missing channel on "home"; the super-admin guard sends a signed-out visitor to the login page; a message you just sent scrolls into view and the unread marker is placed only after a real last-read message; the feed's top sits below the fixed header and strip; quote previews keep link titles; same-document #message links scroll; the support box clears its alerts and validates the email format in place; the global-ad width field loads 300 instead of 0; storage figures read left-to-right; the channels table keeps its action icons on one line and shows filter-specific empty states; toasts fit a phone width; login and about tap targets meet 40px; the copy-link fallback shows the link to copy by hand.
- Re-verification of those 28 in the browser found four residuals, fixed: the create form judged "address typed" on the raw input, so a field that only ever held Hebrew or punctuation blocked the name from deriving the address; a signed-in visitor navigating in-app to a missing/disabled channel still mounted the chat shell and fired eight requests (the route guard now settles the /info probe for signed-in visitors too) while the card itself lost its signed-in buttons (the session is loaded on the card as well); the feed's top padding sat inside a Sass `@if meta.variable-exists(--input-height)` that never emitted (a CSS custom property is not a Sass variable), so the first day separator and message still sat under the strip; and a session that ends mid-edit in the super-admin panel or the manage page now says so and goes to sign in (new `sessionExpiredInterceptor`), instead of a generic "save failed" — with the cached user dropped first, or the login page bounced the operator straight back in a loop.

