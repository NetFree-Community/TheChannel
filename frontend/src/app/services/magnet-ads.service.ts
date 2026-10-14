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
  /**
   * True when the super admin locked magnet ads (globally or for this
   * channel): the values above are then the global ones and the channel's own
   * settings are ignored. Optional — an older backend omits it.
   */
  locked?: boolean;
}

@Injectable({ providedIn: 'root' })
export class MagnetAdsService {
  private settings: MagnetSettings | null = null;
  private settingsPromise: Promise<MagnetSettings | null> | null = null;

  /**
   * Slot decisions already taken, by message id, for the channel in
   * `decisionsSlug`. A slot is placed by counting messages or seconds from
   * the previous slot, and counting afresh from the oldest LOADED message
   * meant every older page prepended by a scroll-up shifted every count: the
   * slots landed on different messages (0/6 overlap for per=3 on a
   * 20-message page), each moved slot destroyed and recreated its
   * <app-magnet-ad-slot>, and the snippet ran again in a fresh frame. A
   * decision, once taken, is replayed on every later fill and only undecided
   * messages are placed, so a slot never moves once it is on screen. The map
   * is dropped with the channel and whenever the placement settings change.
   */
  private decisions = new Map<number, boolean>();
  private decisionsSlug: string | undefined;
  private viewerIds = new Map<string, string>();

  constructor(private slugService: SlugService) {}

  clearCache() {
    this.settings = null;
    this.settingsPromise = null;
    this.decisions.clear();
    this.decisionsSlug = undefined;
  }

  loadSettings(force = false): Promise<MagnetSettings | null> {
    if (this.settings && !force) return Promise.resolve(this.settings);
    if (this.settingsPromise && !force) return this.settingsPromise;

    this.settingsPromise = fetch(`/api/channel/${this.slugService.slug}/ads/magnet`)
      .then(r => r.ok ? r.json() : null)
      .then((data: MagnetSettings | null) => {
        // Other placement settings would place the loaded window differently,
        // so the decisions taken under the old ones are dropped. A re-read
        // that brings the same values keeps them: recomputing from scratch
        // would move the slots a prepended page had been placed around.
        if (this.placementKey(data) !== this.placementKey(this.settings)) this.decisions.clear();
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
   * A random id for this browser in this channel, handed to every ad frame
   * as Magnet's viewer id (see storageShim in magnet-ad-slot). The frames
   * have an opaque origin and no storage of their own, so without it Magnet
   * saw every slot as a new viewer. Kept per channel so that the snippets of
   * different channel owners, which can all read it, cannot use it to link
   * one reader across channels.
   */
  viewerId(): string {
    const key = `magnet_vid:${this.slugService.slug ?? ''}`;
    const known = this.viewerIds.get(key);
    if (known) return known;
    let id: string | null = null;
    try {
      const stored = localStorage.getItem(key);
      if (stored && /^[0-9a-f]{24}$/.test(stored)) id = stored;
    } catch { /* storage blocked: the in-memory id still covers this visit */ }
    if (!id) {
      const bytes = new Uint8Array(12);
      crypto.getRandomValues(bytes);
      id = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
      try { localStorage.setItem(key, id); } catch { /* see above */ }
    }
    this.viewerIds.set(key, id);
    return id;
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

    // Decisions belong to one channel's id sequence. clearCache() drops them
    // on a switch; this guard covers a fill that arrives before it.
    const slug = this.slugService.slug;
    if (slug !== this.decisionsSlug) {
      this.decisions.clear();
      this.decisionsSlug = slug;
    }

    const chrono = [...messages].reverse();

    // 'per_seconds' is a legacy value stored by an older super-admin form.
    if (s.mode === 'by_time' || s.mode === 'per_seconds') {
      this.fillByTime(result, chrono, s);
    } else {
      this.fillByMessages(result, chrono, s);
    }

    return result;
  }

  /**
   * The placement-relevant settings as a comparable string; the snippet is
   * left out because a new snippet does not move slots.
   */
  private placementKey(s: MagnetSettings | null): string {
    if (!s) return '';
    return [s.mode, s.perMessages, s.minTimeSeconds, s.perSeconds, s.minMessagesSinceLast].join('|');
  }

  /**
   * Both fillers walk the loaded window oldest-first with the documented
   * rules (SET.md, "תדירות הצגה"): a slot every `per` visible messages, or
   * every `per` seconds, counted from the previous slot, each honouring its
   * min-gap setting. A message already in `decisions` replays its decision
   * and feeds the counters as if placed now; an undecided one is decided by
   * the rules and remembered. A window that grows at its newest end is thus
   * a plain continuation. An older page prepended by a scroll-up sits BEFORE
   * the decided messages in the walk and is placed from its own start, so
   * the decided slots never move; only the boundary between that page and
   * the old window can fall short of the gap — the page's last slot and the
   * window's first may be closer than `per` (or than the min-gap setting) —
   * which is accepted, the alternative being slots that move on screen.
   */
  private fillByMessages(out: Set<number>, chrono: ChatMessage[], s: MagnetSettings): void {
    const per = Math.max(1, s.perMessages || 5);
    const minTime = Math.max(0, s.minTimeSeconds || 0);

    let countSinceLast = 0;
    let lastAdTime: number | null = null;

    for (const m of chrono) {
      countSinceLast++;
      const id = m.id ?? null;
      const msgTime = this.toEpoch(m.timestamp);

      let place = id !== null ? this.decisions.get(id) : undefined;
      if (place === undefined) {
        const enoughTimePassed = !minTime || lastAdTime === null ||
          (msgTime !== null && (msgTime - lastAdTime) >= minTime * 1000);
        place = id !== null && countSinceLast >= per && enoughTimePassed;
        if (id !== null) this.decisions.set(id, place);
      }

      if (place && id !== null) {
        out.add(id);
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

      const id = m.id ?? null;
      let place = id !== null ? this.decisions.get(id) : undefined;
      if (place === undefined) {
        // The oldest timed message of a fresh walk only starts the clock.
        place = id !== null && lastAdTime !== null &&
          (msgTime - lastAdTime) / 1000 >= per && msgsSinceLast >= minMsgs;
        if (id !== null) this.decisions.set(id, place);
      }

      if (place && id !== null) {
        out.add(id);
        lastAdTime = msgTime;
        msgsSinceLast = 0;
      } else if (lastAdTime === null) {
        lastAdTime = msgTime;
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
