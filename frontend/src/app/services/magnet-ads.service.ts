import { Injectable } from '@angular/core';
import { ChatMessage } from './chat.service';
import { SlugService } from './slug.service';

export interface MagnetSettings {
  enabled: boolean;
  snippet: string;
  mode: 'by_messages' | 'by_time' | string;
  perMessages: number;
  minTimeSeconds: number;
  perSeconds: number;
  minMessagesSinceLast: number;
}

@Injectable({ providedIn: 'root' })
export class MagnetAdsService {
  private settings: MagnetSettings | null = null;
  private settingsPromise: Promise<MagnetSettings | null> | null = null;

  constructor(private slugService: SlugService) {}

  clearCache() {
    this.settings = null;
    this.settingsPromise = null;
  }

  loadSettings(force = false): Promise<MagnetSettings | null> {
    if (this.settings && !force) return Promise.resolve(this.settings);
    if (this.settingsPromise && !force) return this.settingsPromise;

    this.settingsPromise = fetch(`/api/channel/${this.slugService.slug}/ads/magnet`)
      .then(r => r.ok ? r.json() : null)
      .then((data: MagnetSettings | null) => {
        this.settings = data;
        return data;
      })
      .catch(() => null);

    return this.settingsPromise;
  }

  getSettings(): MagnetSettings | null {
    return this.settings;
  }

  /**
   * Compute which message IDs should have an ad slot rendered after them.
   * Input: messages array as held in chat.component (newest at index 0).
   * Output: a Set of message IDs. The chat template iterates messages and,
   * for any message whose id is in this set, renders an <app-magnet-ad-slot>
   * directly below it. The list is rendered by a flex-column-reverse
   * container so the slots appear visually after the message in chronological
   * reading order.
   */
  computeAdSlots(messages: ChatMessage[]): Set<number> {
    const result = new Set<number>();
    if (!messages || messages.length === 0) return result;

    const s = this.settings;
    if (!s || !s.enabled || !s.snippet?.trim()) return result;

    const chrono = [...messages].reverse();

    // 'per_seconds' is a legacy value stored by an older super-admin form.
    if (s.mode === 'by_time' || s.mode === 'per_seconds') {
      this.fillByTime(result, chrono, s);
    } else {
      this.fillByMessages(result, chrono, s);
    }

    return result;
  }

  private fillByMessages(out: Set<number>, chrono: ChatMessage[], s: MagnetSettings): void {
    const per = Math.max(1, s.perMessages || 5);
    const minTime = Math.max(0, s.minTimeSeconds || 0);

    let countSinceLast = 0;
    let lastAdTime: number | null = null;

    for (const m of chrono) {
      countSinceLast++;
      if (countSinceLast < per) continue;

      const msgTime = this.toEpoch(m.timestamp);
      const enoughTimePassed = !minTime || lastAdTime === null ||
        (msgTime !== null && (msgTime - lastAdTime) >= minTime * 1000);

      if (enoughTimePassed && m.id !== undefined && m.id !== null) {
        out.add(m.id);
        countSinceLast = 0;
        if (msgTime !== null) lastAdTime = msgTime;
      }
    }
  }

  private fillByTime(out: Set<number>, chrono: ChatMessage[], s: MagnetSettings): void {
    const per = Math.max(1, s.perSeconds || 60);
    const minMsgs = Math.max(0, s.minMessagesSinceLast || 0);

    let lastAdTime: number | null = null;
    let msgsSinceLast = 0;

    for (const m of chrono) {
      msgsSinceLast++;

      const msgTime = this.toEpoch(m.timestamp);
      if (msgTime === null) continue;

      if (lastAdTime === null) {
        lastAdTime = msgTime;
        continue;
      }

      const elapsed = (msgTime - lastAdTime) / 1000;
      if (elapsed >= per && msgsSinceLast >= minMsgs && m.id !== undefined && m.id !== null) {
        out.add(m.id);
        lastAdTime = msgTime;
        msgsSinceLast = 0;
      }
    }
  }

  private toEpoch(ts: any): number | null {
    if (!ts) return null;
    if (ts instanceof Date) return ts.getTime();
    const t = new Date(ts).getTime();
    return isNaN(t) ? null : t;
  }
}
