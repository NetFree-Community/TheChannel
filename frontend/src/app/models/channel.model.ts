// Operator-controlled toggles reported by GET /api/channel/{slug}/info. Every
// key is optional: an older backend (or a stale cached response) omits them, and
// the UI must then behave as if the features are on rather than hiding controls.
export interface ChannelFeatureFlags {
  reactions?: boolean;
  fileUploads?: boolean;
  reports?: boolean;
  scheduledMessages?: boolean;
}

export interface Channel {
  // The backend serialises the channel slug into the "id" field (as a string).
  id?: string;
  name?: string;
  description?: string;
  created_at?: string;
  logoUrl?: string;
  views?: number;
  require_auth_for_view_files?: boolean;
  contact_us?: string;
  features?: ChannelFeatureFlags;
  // Search-engine settings (backend/channel_seo.go). Absent means listed,
  // with the name and description as the search result's text.
  require_auth?: boolean;
  seo_hidden?: boolean;
  seo_title?: string;
  seo_description?: string;
}

/** The search part of a channel edit, as the backend's request names it. */
export interface ChannelSeoSettings {
  seoHidden: boolean;
  seoTitle: string;
  seoDescription: string;
}
