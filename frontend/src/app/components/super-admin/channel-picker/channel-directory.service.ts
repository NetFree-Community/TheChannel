import { Injectable } from '@angular/core';
import { ChannelData, SuperAdminService } from '../../../services/super-admin.service';

export type ChannelDirectoryState = 'idle' | 'loading' | 'ready' | 'error';

/**
 * The list of channels for the super-admin channel pickers, shared by the
 * picker (to search it) and its host screen (to mark saved slugs that no
 * longer exist). Read from GET /api/super-admin/channels.
 */
@Injectable({ providedIn: 'root' })
export class ChannelDirectoryService {
  state: ChannelDirectoryState = 'idle';
  channels: ChannelData[] = [];
  private slugs = new Set<string>();
  private pending: Promise<void> | null = null;

  constructor(private superAdminService: SuperAdminService) {}

  /**
   * Fetches the list again (channels may have been added since the last
   * visit). A list that is already loaded stays usable while refreshing and
   * is kept if the refresh fails.
   */
  refresh(): Promise<void> {
    if (this.pending) return this.pending;
    if (this.state !== 'ready') this.state = 'loading';
    this.pending = this.superAdminService.getChannels()
      .then(list => {
        this.channels = (list || []).filter(c => !!c?.slug);
        this.slugs = new Set(this.channels.map(c => c.slug));
        this.state = 'ready';
      })
      .catch(() => {
        if (this.state !== 'ready') this.state = 'error';
      })
      .finally(() => this.pending = null);
    return this.pending;
  }

  /** True only when the list is loaded and the slug is not in it. */
  isMissing(slug: string): boolean {
    return this.state === 'ready' && !this.slugs.has(slug);
  }
}
