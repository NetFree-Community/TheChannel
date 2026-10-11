import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable, firstValueFrom } from 'rxjs';

export type SlugUnavailableReason = 'taken' | 'invalid' | 'reserved' | '';

export interface SlugAvailability {
  available: boolean;
  reason: SlugUnavailableReason;
}

export interface CreatedChannel {
  slug: string;
  name: string;
}

/**
 * Mirror of the backend slug rule (`^[a-z0-9][a-z0-9\-]{1,48}[a-z0-9]$`): 3–50
 * characters, lowercase alphanumerics and hyphens, never starting or ending
 * with a hyphen. Kept here so the form can pre-validate before spending a
 * round-trip on /slug-available.
 */
export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$/;
export const SLUG_MIN_LENGTH = 3;
export const SLUG_MAX_LENGTH = 50;

/**
 * Mirrors of backend/channel_create.go (maxChannelNameLen, maxChannelDescLen,
 * maxChannelsPerOwner). The form counts characters exactly like the server
 * does, so a Hebrew name is never cut short of the advertised limit.
 */
export const CHANNEL_NAME_MAX = 80;
export const CHANNEL_DESCRIPTION_MAX = 2000;
export const MAX_CHANNELS_PER_ACCOUNT = 5;

/**
 * Letter pairs written with a geresh to spell sounds Hebrew has no letter for.
 * Matched before the single-letter table, so ג׳ becomes "j" rather than "g".
 */
const HEBREW_DIGRAPHS: Record<string, string> = {
  'ג׳': 'j',
  'ז׳': 'zh',
  'צ׳': 'ch',
  'ד׳': 'dh',
  'ת׳': 'th',
};

/**
 * Hebrew letters to Latin. Unpointed Hebrew omits most vowels, so a faithful
 * transliteration is impossible — this aims for a recognisable, stable starting
 * point the user can edit, not scholarly romanisation. ו maps to "o" and י to
 * "i" because in unpointed text they far more often carry a vowel than the
 * consonants "v"/"y", which keeps words like הבוקר readable ("hboker", not
 * "hbvkr"). Final forms map to the same letter as their regular form.
 */
const HEBREW_LETTERS: Record<string, string> = {
  'א': 'a', 'ב': 'b', 'ג': 'g', 'ד': 'd', 'ה': 'h', 'ו': 'o', 'ז': 'z',
  'ח': 'ch', 'ט': 't', 'י': 'i', 'כ': 'k', 'ך': 'k', 'ל': 'l', 'מ': 'm',
  'ם': 'm', 'נ': 'n', 'ן': 'n', 'ס': 's', 'ע': 'a', 'פ': 'p', 'ף': 'p',
  'צ': 'tz', 'ץ': 'tz', 'ק': 'k', 'ר': 'r', 'ש': 'sh', 'ת': 't',
};

/**
 * Rewrites Hebrew text to Latin so a Hebrew channel name still yields a usable
 * slug. Anything already Latin passes through untouched.
 */
export function transliterateHebrew(input: string): string {
  // Niqqud and cantillation first, so pointed text maps exactly like the
  // unpointed spelling of the same word.
  let s = (input || '').replace(/[֑-ׇ]/g, '');
  // Maqaf is Hebrew's hyphen; typed apostrophes and the curly quote all stand
  // in for a geresh, so normalise them to one form for the digraph lookup.
  s = s.replace(/־/g, '-').replace(/['’]/g, '׳');

  let out = '';
  for (let i = 0; i < s.length; i++) {
    const digraph = HEBREW_DIGRAPHS[s.slice(i, i + 2)];
    if (digraph) {
      out += digraph;
      i++;
      continue;
    }
    out += HEBREW_LETTERS[s[i]] ?? s[i];
  }
  return out;
}

/**
 * Derives a slug suggestion from a free-text channel name: transliterated to
 * Latin, lowercased, spaces to hyphens, anything outside [a-z0-9-] dropped.
 * The length cap is applied before the final hyphen trim, or truncating mid
 * word could leave a trailing hyphen and produce a slug the backend rejects.
 */
export function slugifyChannelName(name: string): string {
  return transliterateHebrew(name)
    .toLowerCase()
    .trim()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, SLUG_MAX_LENGTH)
    .replace(/^-+|-+$/g, '');
}

/**
 * Normalises what the user types into the slug field as they type, so the
 * field can never hold an illegal character (uppercase, spaces, Hebrew).
 */
export function sanitizeSlugInput(value: string): string {
  return (value || '').toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '');
}

/**
 * The n-th fallback address for a taken slug: `news` → `news-2`, `news-3`…
 * The base is trimmed first so the suffix never pushes the result past the
 * length limit, and a trailing hyphen left by the trim is dropped so the
 * variant is always a valid slug.
 */
export function slugVariant(base: string, n: number): string {
  const suffix = `-${n}`;
  const room = SLUG_MAX_LENGTH - suffix.length;
  const trimmed = base.replace(/-\d+$/, '').slice(0, room).replace(/-+$/g, '');
  return `${trimmed}${suffix}`;
}

/**
 * Seconds-to-minutes of a 429's Retry-After header, rounded up, or null when
 * the server sent none. The creation limiter refills one slot every 20
 * minutes, so the number shown is the real wait rather than a guess.
 */
export function retryAfterMinutes(err: any): number | null {
  const raw = err?.headers?.get?.('Retry-After');
  const seconds = Number(raw);
  if (!raw || !Number.isFinite(seconds) || seconds <= 0) return null;
  return Math.max(1, Math.ceil(seconds / 60));
}

@Injectable({
  providedIn: 'root'
})
export class ChannelService {

  constructor(private http: HttpClient) {}

  /**
   * Instant channel creation. The owner is taken from the session on the
   * backend, so there is deliberately no owner field here.
   * 400 invalid slug / missing name · 409 slug taken · 403 channel limit ·
   * 429 rate limited — all answered as plain text in the body.
   */
  createChannel(slug: string, name: string, description: string): Promise<CreatedChannel> {
    return firstValueFrom(
      this.http.post<CreatedChannel>('/api/channels/create', { slug, name, description })
    );
  }

  /** Left as an Observable so callers can debounce/switchMap the keystrokes. */
  checkSlugAvailability(slug: string): Observable<SlugAvailability> {
    return this.http.get<SlugAvailability>('/api/channels/slug-available', { params: { slug } });
  }

  /**
   * The first free `base-N` variant of a taken slug, or null when none of the
   * first `maxTries` is free (or the checks failed). Sequential on purpose:
   * the availability probe is rate limited per user, and the first hit ends
   * the search — usually after a single request.
   */
  async findFreeSlugVariant(base: string, maxTries = 5): Promise<string | null> {
    for (let n = 2; n < 2 + maxTries; n++) {
      const candidate = slugVariant(base, n);
      if (!SLUG_PATTERN.test(candidate)) return null;
      try {
        const result = await firstValueFrom(this.checkSlugAvailability(candidate));
        if (result?.available) return candidate;
      } catch {
        // A failed probe (rate limit, network) is not worth a second one.
        return null;
      }
    }
    return null;
  }
}
