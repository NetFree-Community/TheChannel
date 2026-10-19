import { Injectable } from '@angular/core';

export interface ShareData {
  title?: string;
  text?: string;
  url: string;
}

export type ShareOutcome = 'shared' | 'cancelled' | 'unsupported';

/**
 * Everything that hands a channel link to someone else: the link itself, the
 * clipboard, WhatsApp and the OS share sheet. One service, so every screen
 * that offers "share" (the create-success card, my channels, the channel
 * header) behaves the same and fails the same way. The clipboard in particular
 * is unavailable on plain HTTP and inside some in-app browsers, where a button
 * flipping to "copied" with nothing copied is worse than no button.
 */
@Injectable({ providedIn: 'root' })
export class ShareService {
  /** The direct, shareable URL of a channel — never a hardcoded domain. */
  channelUrl(slug: string): string {
    return `${window.location.origin}/channel/${slug}`;
  }

  /** Whether the OS share sheet (Web Share API) exists here. */
  get canShare(): boolean {
    return typeof navigator !== 'undefined' && typeof navigator.share === 'function';
  }

  /**
   * Copies text to the clipboard and resolves true on success. On failure it
   * hands the user the text itself to copy by hand — in a prompt, which shows
   * it preselected and works on HTTP and in in-app browsers; a toast that
   * only said "select the link" pointed at a link most callers never show —
   * unless `quiet` is set.
   */
  async copy(text: string, opts: { quiet?: boolean } = {}): Promise<boolean> {
    try {
      // `navigator.clipboard?.writeText(...)` resolves to undefined in an
      // insecure context, which an `await` happily treats as success.
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      if (!opts.quiet) {
        window.prompt('ההעתקה נחסמה בדפדפן — העתיקו את הקישור ידנית:', text);
      }
      return false;
    }
  }

  /** A WhatsApp "send to anyone" link carrying the given text. */
  whatsappUrl(text: string): string {
    return `https://wa.me/?text=${encodeURIComponent(text)}`;
  }

  /** The invitation sentence used by every share button, so it reads the same everywhere. */
  inviteText(channelName: string, url: string): string {
    const name = channelName?.trim();
    return name ? `הצטרפו לערוץ "${name}": ${url}` : `הצטרפו לערוץ: ${url}`;
  }

  /** Opens the OS share sheet. Never throws; the caller decides what to do on 'unsupported'. */
  async share(data: ShareData): Promise<ShareOutcome> {
    if (!this.canShare) return 'unsupported';
    try {
      await navigator.share(data);
      return 'shared';
    } catch (err: any) {
      // The user closing the sheet is not an error worth a toast.
      return err?.name === 'AbortError' ? 'cancelled' : 'unsupported';
    }
  }
}
