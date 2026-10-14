/**
 * Magnet's site-stats answer, turned into what the super-admin statistics
 * screen draws. Pure functions only, so the shape rules live in one place.
 *
 * Two shapes reach this file. The original one carries six numbers (clicks and
 * earnings for today, 7 days and 30 days). The extended one (hebrew-ad-flow
 * site_stats_report_by_key, 14.10.2026) adds impressions, CTR, a 30-day daily
 * series and per-ad / per-hour / per-device breakdowns after those six,
 * unchanged. Everything the old shape lacks comes out null here, and the
 * screen hides what is null — so this side can ship before or after Magnet.
 */

export type MagnetRange = 'today' | 'week' | 'month';

export interface MagnetSummary {
  clicks: number;
  earnings: number;
  /** null: not in the answer, or not kept that long (impressions: 7 days). */
  impressions: number | null;
  /** A fraction (0.02 = 2%); null when there is nothing to divide by. */
  ctr: number | null;
}

export interface MagnetDay {
  /** YYYY-MM-DD, an Israel calendar day. */
  date: string;
  clicks: number;
  earnings: number;
  /** null: the day is older than Magnet keeps impressions. */
  impressions: number | null;
}

export interface MagnetAdRow {
  id: string;
  title: string;
  clicks: number;
  earnings: number;
  impressionsWeek: number;
  clicksWeek: number;
  ctrWeek: number | null;
}

export interface MagnetPlacementRow {
  /** The slot's label as Magnet stored it (a channel slug). */
  name: string;
  clicks: number;
  earnings: number;
}

export interface MagnetDevices {
  mobile: number;
  tablet: number;
  desktop: number;
  unknown: number;
}

export interface MagnetStatsView {
  domain: string;
  generatedAt: Date | null;
  summary: Record<MagnetRange, MagnetSummary>;
  totalEarnings: number | null;
  totalClicks: number | null;
  balance: number | null;
  /** How many days back Magnet keeps impressions. */
  impressionsDays: number;
  daily: MagnetDay[] | null;
  byAd: MagnetAdRow[] | null;
  byHour: number[] | null;
  byDevice: MagnetDevices | null;
  /** Not sent by Magnet yet; drawn only once it is. */
  byPlacement: MagnetPlacementRow[] | null;
}

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function obj(v: unknown): Obj {
  return isObj(v) ? v : {};
}

/** A finite number, or 0. */
export function num(v: unknown): number {
  const n = typeof v === 'number' ? v : Number(v);
  return v !== null && v !== '' && Number.isFinite(n) ? n : 0;
}

/** A finite number, or null when absent or not a number. */
export function numOrNull(v: unknown): number | null {
  if (v === null || v === undefined || v === '') return null;
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) ? n : null;
}

export function ratio(clicks: number, impressions: number | null): number | null {
  return impressions && impressions > 0 ? clicks / impressions : null;
}

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** Normalises either shape. Returns null for something that is not stats. */
export function toMagnetStatsView(raw: unknown): MagnetStatsView | null {
  if (!isObj(raw)) return null;
  const clicks = obj(raw['clicks']);
  const earnings = obj(raw['earnings']);
  if (!isObj(raw['clicks']) && !isObj(raw['earnings'])) return null;

  const imp = isObj(raw['impressions']) ? raw['impressions'] : null;
  const ctr = isObj(raw['ctr']) ? raw['ctr'] : null;

  const summaryFor = (range: MagnetRange): MagnetSummary => {
    const c = num(clicks[range]);
    const i = imp ? numOrNull(imp[range]) : null;
    const sentCtr = ctr ? numOrNull(ctr[range]) : null;
    return {
      clicks: c,
      earnings: num(earnings[range]),
      impressions: i,
      ctr: sentCtr ?? ratio(c, i),
    };
  };

  const generated = typeof raw['generated_at'] === 'string' ? new Date(raw['generated_at']) : null;

  return {
    domain: String(obj(raw['site'])['domain'] ?? ''),
    generatedAt: generated && !isNaN(generated.getTime()) ? generated : null,
    summary: { today: summaryFor('today'), week: summaryFor('week'), month: summaryFor('month') },
    totalEarnings: numOrNull(earnings['total']),
    totalClicks: numOrNull(clicks['total']),
    balance: numOrNull(raw['balance']),
    impressionsDays: numOrNull(raw['impressions_days']) || 7,
    daily: parseDaily(raw['daily']),
    byAd: parseByAd(raw['by_ad']),
    byHour: parseByHour(raw['by_hour']),
    byDevice: parseDevices(raw['by_device']),
    byPlacement: parsePlacements(raw['by_placement']),
  };
}

function parseDaily(v: unknown): MagnetDay[] | null {
  if (!Array.isArray(v)) return null;
  const days = v
    .filter((d): d is Obj => isObj(d) && typeof d['date'] === 'string' && DATE_RE.test(d['date'] as string))
    .map((d) => ({
      date: d['date'] as string,
      clicks: num(d['clicks']),
      earnings: num(d['earnings']),
      impressions: numOrNull(d['impressions']),
    }))
    .sort((a, b) => a.date.localeCompare(b.date));
  return days.length ? days : null;
}

function parseByAd(v: unknown): MagnetAdRow[] | null {
  if (!Array.isArray(v)) return null;
  return v.filter(isObj).map((a, i) => {
    const impressionsWeek = num(a['impressions_week']);
    const clicksWeek = num(a['clicks_week']);
    return {
      id: String(a['id'] ?? i),
      title: String(a['title'] ?? '').trim(),
      clicks: num(a['clicks']),
      earnings: num(a['earnings']),
      impressionsWeek,
      clicksWeek,
      ctrWeek: numOrNull(a['ctr_week']) ?? ratio(clicksWeek, impressionsWeek),
    };
  });
}

function parseByHour(v: unknown): number[] | null {
  if (!Array.isArray(v) || v.length === 0) return null;
  return Array.from({ length: 24 }, (_, h) => num(v[h]));
}

function parseDevices(v: unknown): MagnetDevices | null {
  if (!isObj(v)) return null;
  return { mobile: num(v['mobile']), tablet: num(v['tablet']), desktop: num(v['desktop']), unknown: num(v['unknown']) };
}

function parsePlacements(v: unknown): MagnetPlacementRow[] | null {
  if (!Array.isArray(v)) return null;
  const rows = v.filter(isObj).map((p) => ({
    name: String(p['placement'] ?? p['name'] ?? '').trim(),
    clicks: num(p['clicks']),
    earnings: num(p['earnings']),
  })).filter((p) => p.name);
  return rows.length ? rows : null;
}

/** The last n days of the series (the whole of it when n is larger). */
export function lastDays(daily: MagnetDay[], n: number): MagnetDay[] {
  return daily.slice(Math.max(0, daily.length - n));
}

/**
 * A round ceiling for a chart axis: 1, 2 or 5 times a power of ten, at least
 * the largest value. 0 for an empty series, so the caller can tell.
 */
export function niceMax(max: number): number {
  if (!(max > 0)) return 0;
  const pow = Math.pow(10, Math.floor(Math.log10(max)));
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (step * pow >= max) return step * pow;
  }
  return 10 * pow;
}

// ─── Hebrew formatting ────────────────────────────────────────────────────

const intFmt = new Intl.NumberFormat('he-IL', { maximumFractionDigits: 0 });
const moneyFmt = new Intl.NumberFormat('he-IL', { style: 'currency', currency: 'ILS', minimumFractionDigits: 2, maximumFractionDigits: 2 });
const moneyShortFmt = new Intl.NumberFormat('he-IL', { style: 'currency', currency: 'ILS', maximumFractionDigits: 0 });
const pctFmt = new Intl.NumberFormat('he-IL', { style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 2 });

export function fmtInt(n: number): string {
  return intFmt.format(n);
}

export function fmtMoney(n: number): string {
  return moneyFmt.format(n);
}

/** Whole shekels, for axis labels. */
export function fmtMoneyShort(n: number): string {
  return moneyShortFmt.format(n);
}

export function fmtPct(n: number | null): string {
  return n === null ? '—' : pctFmt.format(n);
}

/** A YYYY-MM-DD calendar date as a Date at noon UTC, so no zone shifts the day. */
function dayDate(date: string): Date {
  return new Date(`${date}T12:00:00Z`);
}

const dayLongFmt = new Intl.DateTimeFormat('he-IL', { weekday: 'long', day: 'numeric', month: 'long', timeZone: 'UTC' });
const dayShortFmt = new Intl.DateTimeFormat('he-IL', { day: 'numeric', month: 'numeric', timeZone: 'UTC' });

/** "יום שלישי, 14 באוקטובר" */
export function fmtDayLong(date: string): string {
  return dayLongFmt.format(dayDate(date));
}

/** "14.10" */
export function fmtDayShort(date: string): string {
  return dayShortFmt.format(dayDate(date));
}

const rtf = new Intl.RelativeTimeFormat('he', { numeric: 'auto' });

/** "עכשיו", "לפני 5 דקות", "לפני שעתיים", "אתמול". */
export function fmtAgo(then: Date, now: Date): string {
  const sec = Math.max(0, Math.round((now.getTime() - then.getTime()) / 1000));
  if (sec < 45) return 'עכשיו';
  const min = Math.round(sec / 60);
  if (min < 60) return rtf.format(-min, 'minute');
  const hours = Math.round(min / 60);
  if (hours < 24) return rtf.format(-hours, 'hour');
  return rtf.format(-Math.round(hours / 24), 'day');
}
