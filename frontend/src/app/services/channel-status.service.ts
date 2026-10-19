import { Injectable, signal } from '@angular/core';

/**
 * Central holder for the "this channel was switched off by an operator" state.
 *
 * A disabled channel makes every /api/channel/{slug}/* call answer 403 with
 * {"error":"channel_disabled"}. That hits many callers at once (chat, ads,
 * notifications, user-info…), so the detection lives in the HTTP interceptor
 * (channel-disabled.interceptor.ts) and lands here; the channel view renders
 * off this signal instead of every caller growing its own error branch.
 */
@Injectable({ providedIn: 'root' })
export class ChannelStatusService {
  private readonly _disabledSlug = signal<string | null>(null);
  private readonly _notFoundSlug = signal<string | null>(null);

  /** Slug of the channel that answered channel_disabled, or null. */
  readonly disabledSlug = this._disabledSlug.asReadonly();

  /**
   * Slug whose /info answered 404, or null. A mistyped or deleted channel
   * address used to render the full chat shell with a "loading failed"
   * banner, as if the server were down; this lets the page say plainly that
   * there is no such channel.
   */
  readonly notFoundSlug = this._notFoundSlug.asReadonly();

  markDisabled(slug: string): void {
    if (this._disabledSlug() !== slug) {
      this._disabledSlug.set(slug);
    }
  }

  markNotFound(slug: string): void {
    if (this._notFoundSlug() !== slug) {
      this._notFoundSlug.set(slug);
    }
  }

  /** Called when navigating to another channel so a stale flag cannot leak. */
  reset(): void {
    if (this._disabledSlug() !== null) {
      this._disabledSlug.set(null);
    }
    if (this._notFoundSlug() !== null) {
      this._notFoundSlug.set(null);
    }
  }
}
