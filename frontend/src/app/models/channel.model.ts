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
  id?: number;
  slug?: string;
  name?: string;
  description?: string;
  created_at?: string;
  logoUrl?: string;
  views?: number;
  require_auth_for_view_files?: boolean;
  contact_us?: string;
  features?: ChannelFeatureFlags;
}
