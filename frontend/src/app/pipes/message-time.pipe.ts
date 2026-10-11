import { Pipe, PipeTransform } from '@angular/core';

// Built once: Intl formatters are expensive to construct and the pipe runs
// for every message on every change-detection pass.
const TIME = new Intl.DateTimeFormat('he-IL', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const WEEKDAY = new Intl.DateTimeFormat('he-IL', { weekday: 'long' });
const DAY_MS = 86_400_000;

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** Whole calendar days from `now` to `d` (negative = past), in local time. */
export function calendarDayDiff(d: Date, now: Date = new Date()): number {
  return Math.round((startOfDay(d) - startOfDay(now)) / DAY_MS);
}

export function formatDayMonthYear(d: Date): string {
  return `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()}`;
}

export function formatWeekday(d: Date): string {
  return WEEKDAY.format(d);
}

/** The absolute moment, "3.3.2026 14:05" — the tooltip behind every relative label. */
export function formatFullDateTime(d: Date): string {
  return `${formatDayMonthYear(d)} ${TIME.format(d)}`;
}

/**
 * Relative, Hebrew message time: "14:05" today, "אתמול 14:05", the weekday
 * within the last six days, "מחר 14:05" / weekday / full date for scheduled
 * (future) entries, and "3.3.2026 14:05" otherwise. The same calendar rules
 * moment's he locale applied, without the ~70 KB moment bundle: Intl is in
 * every browser already.
 */
@Pipe({
  name: 'messageTime'
})
export class MessageTimePipe implements PipeTransform {

  transform(value: unknown): string {
    if (value === null || value === undefined || value === '') return '';
    const d = value instanceof Date ? value : new Date(value as string | number);
    if (isNaN(d.getTime())) return '';

    const time = TIME.format(d);
    const diff = calendarDayDiff(d);
    if (diff === 0) return time;
    if (diff === -1) return `אתמול ${time}`;
    if (diff === 1) return `מחר ${time}`;
    if (diff >= -6 && diff <= 6) return `${WEEKDAY.format(d)} ${time}`;
    return `${formatDayMonthYear(d)} ${time}`;
  }

}
