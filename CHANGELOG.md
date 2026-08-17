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
| `frontend/src/app/components/super-admin/channels/channel-features.component.*` | Per-channel feature toggles (12 features) |
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
- `enforceStorageQuota` — checks quota before upload; runs auto-cleanup (target: 80% usage) if enabled
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
/api/user-info

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

/api/channel/{slug}/import/post  (API key auth)

/api/channel/{slug}/*  (channelMiddleware + channelIfRequireAuth)
  GET  /info
  GET  /messages
  GET  /events
  GET  /files/{fileid}
  POST /files
  GET  /emojis/list
  GET  /notifications-config
  GET  /ads/settings
  GET  /ads/magnet
  (login required):
    POST /notifications-subscribe
    POST /reactions/set-reactions
    POST /messages/report
    GET  /user-info

  /admin/*  (channel owner+)
    PUT  /info
    POST /messages
    PUT  /messages/{id}
    DELETE /messages/{id}
    POST /emojis
    GET/POST /settings
    GET/POST /users
    GET/PUT  /storage
    POST /storage/auto-cleanup
    GET/PUT  /scheduled-messages
    GET  /statistics
    GET/POST /reports
```

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
  - Green (`ok`): < 80% used
  - Orange (`warning`): 80–90% used
  - Red (`critical`): > 90% used
- **Auto-cleanup toggle**: When enabled, automatically deletes oldest files to reach 80% usage before new uploads, ensuring uploads always succeed
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
R2_ACCOUNT_ID=your_account_id
R2_ACCESS_KEY_ID=your_access_key_id
R2_SECRET_ACCESS_KEY=your_secret_access_key
R2_BUCKET_NAME=your_bucket_name
R2_PUBLIC_URL=https://pub-xxxx.r2.dev   # optional, for direct CDN links
```

---

## Dependencies Added

### Go (`backend/go.mod`)
- `github.com/aws/aws-sdk-go-v2` — AWS SDK v2 (used for R2 via S3-compatible API)
- `github.com/aws/aws-sdk-go-v2/credentials`
- `github.com/aws/aws-sdk-go-v2/service/s3`
