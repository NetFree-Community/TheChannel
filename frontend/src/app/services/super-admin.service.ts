import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

export interface ChannelFeatures {
  reactions: boolean;
  fileUploads: boolean;
  reports: boolean;
  ads: boolean;
  notifications: boolean;
  requireAuth: boolean;
  requireAuthFiles: boolean;
  countViews: boolean;
  scheduledMessages: boolean;
  webhook: boolean;
  magnetLockedByAdmin: boolean;
  adsLockedByAdmin: boolean;
}

export interface ChannelData {
  slug: string;
  name: string;
  description: string;
  logoUrl: string;
  ownerEmail: string;
  createdAt: string;
  features: ChannelFeatures;
  contactUs: string;
}

export interface ChannelUser {
  email: string;
  role: 'owner' | 'moderator' | 'writer' | '';
}

export interface GlobalAdsConfig {
  src: string;
  width: number;
  lockAll: boolean;
  lockedChannels: string[];
}

export interface GlobalMagnetConfig {
  enabled: boolean;
  snippet: string;
  mode: string;
  perMessages: number;
  minTimeSeconds: number;
  perSeconds: number;
  minMessagesSinceLast: number;
  apiKey: string;
  lockAll: boolean;
  lockedChannels: string[];
}

export interface SuperAdminUser {
  id: string;
  username: string;
  email: string;
  publicName: string;
  globalRole: string;
  channelRoles: Record<string, string>;
}

export interface Setting {
  key: string;
  value: any;
}

@Injectable({
  providedIn: 'root'
})
export class SuperAdminService {

  constructor(private http: HttpClient) {}

  getChannels(): Promise<ChannelData[]> {
    return firstValueFrom(this.http.get<ChannelData[]>('/api/super-admin/channels'));
  }

  createChannel(slug: string, name: string, ownerEmail: string): Promise<ChannelData> {
    return firstValueFrom(this.http.post<ChannelData>('/api/super-admin/channels/create', { slug, name, ownerEmail }));
  }

  getChannel(slug: string): Promise<ChannelData> {
    return firstValueFrom(this.http.get<ChannelData>(`/api/super-admin/channels/${slug}`));
  }

  deleteChannel(slug: string): Promise<void> {
    return firstValueFrom(this.http.delete<void>(`/api/super-admin/channels/${slug}`));
  }

  updateChannelFeatures(slug: string, features: ChannelFeatures): Promise<void> {
    return firstValueFrom(this.http.put<void>(`/api/super-admin/channels/${slug}/features`, features));
  }

  getChannelUsers(slug: string): Promise<ChannelUser[]> {
    return firstValueFrom(this.http.get<ChannelUser[]>(`/api/super-admin/channels/${slug}/users`));
  }

  setChannelUsers(slug: string, users: ChannelUser[]): Promise<void> {
    return firstValueFrom(this.http.post<void>(`/api/super-admin/channels/${slug}/users`, { users }));
  }

  getGlobalUsers(): Promise<SuperAdminUser[]> {
    return firstValueFrom(this.http.get<SuperAdminUser[]>('/api/super-admin/users/list'));
  }

  setGlobalUsers(users: SuperAdminUser[]): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/users/set', { list: users }));
  }

  getGlobalSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>('/api/super-admin/global-settings/get'));
  }

  setGlobalSettings(settings: Setting[]): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/global-settings/set', settings));
  }

  getAdsConfig(): Promise<GlobalAdsConfig> {
    return firstValueFrom(this.http.get<GlobalAdsConfig>('/api/super-admin/ads/config'));
  }

  setAdsConfig(cfg: GlobalAdsConfig): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/ads/config', cfg));
  }

  getMagnetConfig(): Promise<GlobalMagnetConfig> {
    return firstValueFrom(this.http.get<GlobalMagnetConfig>('/api/super-admin/magnet/config'));
  }

  setMagnetConfig(cfg: GlobalMagnetConfig): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/magnet/config', cfg));
  }

  getMagnetStats(): Promise<any> {
    return firstValueFrom(this.http.get<any>('/api/super-admin/magnet/stats'));
  }

  resetStatistics(): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/statistics/reset', {}));
  }

  getGlobalStorageConfig(): Promise<{ defaultQuotaGb: number }> {
    return firstValueFrom(this.http.get<{ defaultQuotaGb: number }>('/api/super-admin/storage/config'));
  }

  setGlobalStorageConfig(defaultQuotaGb: number): Promise<void> {
    return firstValueFrom(this.http.post<void>('/api/super-admin/storage/config', { defaultQuotaGb }));
  }

  getChannelStorage(slug: string): Promise<any> {
    return firstValueFrom(this.http.get<any>(`/api/super-admin/channels/${slug}/storage`));
  }

  setChannelStorage(slug: string, quotaGb: number): Promise<void> {
    return firstValueFrom(this.http.put<void>(`/api/super-admin/channels/${slug}/storage`, { quotaGb }));
  }

  getChannelRequests(): Promise<ChannelRequest[]> {
    return firstValueFrom(this.http.get<ChannelRequest[]>('/api/super-admin/channel-requests'));
  }

  approveChannelRequest(id: string, slug: string, notes: string): Promise<{ channelSlug: string; ownerEmail: string }> {
    return firstValueFrom(
      this.http.post<{ channelSlug: string; ownerEmail: string }>(
        `/api/super-admin/channel-requests/${id}/approve`,
        { slug, notes }
      )
    );
  }

  rejectChannelRequest(id: string, notes: string): Promise<void> {
    return firstValueFrom(
      this.http.post<void>(`/api/super-admin/channel-requests/${id}/reject`, { notes })
    );
  }
}

export interface ChannelRequest {
  id: string;
  name: string;
  email: string;
  desiredSlug: string;
  description: string;
  status: 'pending' | 'approved' | 'rejected';
  notes: string;
  createdAt: string;
  approvedSlug: string;
}
