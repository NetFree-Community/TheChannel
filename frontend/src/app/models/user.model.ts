export interface User {
  id: string;
  username: string;
  email: string;
  picture: string;
  publicName: string;
  globalRole: string;
  channelRoles: Record<string, string>;
  // Legacy field for backwards compatibility
  privileges?: Record<string, boolean>;
}
