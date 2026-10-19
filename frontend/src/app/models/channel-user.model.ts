// An empty role revokes the user's channel privileges on save.
export interface ChannelUser {
  email: string;
  role: 'owner' | 'moderator' | 'writer' | '';
}
