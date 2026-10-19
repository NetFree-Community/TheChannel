export type ChannelRole = 'owner' | 'moderator' | 'writer';

/** One row of GET /api/my-channels — a channel the signed-in user holds a role on. */
export interface MyChannel {
  slug: string;
  name: string;
  description: string;
  logoUrl: string;
  role: ChannelRole;
  createdAt: string;
  /** Switched off by the platform operator; readers get "הערוץ מושבת". */
  disabled: boolean;
  /** Signed-in readers who ever opened the channel. */
  participants: number;
}

export const ROLE_LABELS: Record<ChannelRole, string> = {
  owner: 'בעלים',
  moderator: 'מנהל',
  writer: 'כותב',
};

/** Whether the role may open the manage page (moderator level and above). */
export function canManage(role: ChannelRole | string | undefined): boolean {
  return role === 'owner' || role === 'moderator';
}
