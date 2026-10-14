import { Component, EventEmitter, Input, OnInit, Output } from '@angular/core';
import { NbButtonModule, NbIconModule, NbInputModule } from '@nebular/theme';
import { ChannelData } from '../../../services/super-admin.service';
import { ChannelDirectoryService } from './channel-directory.service';

export interface ChannelPickerRow {
  channel: ChannelData;
  /** Already in the host's list: shown, but cannot be picked again. */
  taken: boolean;
}

export interface ChannelPickerView {
  rows: ChannelPickerRow[];
  /** Matches left out of `rows` to keep the panel short. */
  hidden: number;
}

/** Rows shown at once; typing more narrows the rest. */
const MAX_ROWS = 50;

/**
 * Lower-cased, without Hebrew niqqud/cantillation or Latin accents, with
 * whitespace collapsed, so "ערוץ" finds "עָרוּץ" and "My Channel" finds
 * "my  channel".
 */
function normalize(s: string): string {
  return (s || '')
    .normalize('NFKD')
    .replace(/[\u0591-\u05BD\u05BF\u05C1\u05C2\u05C4\u05C5\u05C7\u0300-\u036F]/g, '')
    .toLowerCase()
    .replace(/\s+/g, ' ')
    .trim();
}

let nextId = 0;

/**
 * A searchable channel list (ARIA 1.2 combobox + listbox) for picking a
 * channel slug. Searches name and slug. The panel is in the page flow, not an
 * overlay, so it is never clipped by the card around it.
 *
 * If the channel list cannot be loaded, it falls back to typing a slug by
 * hand with an "add" button, like the field it replaced.
 */
@Component({
  selector: 'app-channel-picker',
  standalone: true,
  imports: [NbButtonModule, NbIconModule, NbInputModule],
  templateUrl: './channel-picker.component.html',
  styleUrl: './channel-picker.component.scss',
})
export class ChannelPickerComponent implements OnInit {
  /** The input's id, for the host's <label for>. */
  @Input({ required: true }) inputId!: string;
  /** Slugs already chosen; offered as "already in the list" only. */
  @Input() selected: readonly string[] = [];
  /** A slug the operator picked (never one of `selected`). */
  @Output() picked = new EventEmitter<string>();

  private readonly uid = `channel-picker-${++nextId}`;
  readonly listId = `${this.uid}-list`;
  readonly errorId = `${this.uid}-error`;

  query = '';
  open = false;
  /** The highlighted option (aria-activedescendant), by slug. */
  activeSlug: string | null = null;
  /** Read by screen readers through the polite live region. */
  announcement = '';

  constructor(public directory: ChannelDirectoryService) {}

  ngOnInit(): void {
    this.refreshList();
  }

  /** Loads the list; a panel opened while loading gets its highlight after. */
  private refreshList() {
    this.directory.refresh().then(() => {
      if (this.open) this.syncActive();
    });
  }

  /** The list failed to load: typing a slug by hand is the only way. */
  get manual(): boolean {
    return this.directory.state === 'error';
  }

  get placeholder(): string {
    return this.manual ? 'כתובת הערוץ, למשל my-channel' : 'חיפוש ערוץ לפי שם או כתובת';
  }

  /** Saved slugs that are no longer channels. */
  get missing(): string[] {
    return this.selected.filter(s => this.directory.isMissing(s));
  }

  optionId(slug: string): string {
    return `${this.uid}-opt-${encodeURIComponent(slug)}`;
  }

  view(): ChannelPickerView {
    if (this.directory.state !== 'ready') return { rows: [], hidden: 0 };
    const q = normalize(this.query);
    const taken = new Set(this.selected);
    const scored: { row: ChannelPickerRow; score: number; name: string }[] = [];
    for (const channel of this.directory.channels) {
      const name = normalize(channel.name);
      const slug = normalize(channel.slug);
      let score = 0;
      if (q) {
        if (name.startsWith(q) || slug.startsWith(q)) score = 0;
        else if (name.includes(' ' + q) || slug.includes('-' + q)) score = 1;
        else if (name.includes(q) || slug.includes(q)) score = 2;
        else continue;
      }
      const isTaken = taken.has(channel.slug);
      // Channels already in the list go last: they cannot be picked.
      scored.push({ row: { channel, taken: isTaken }, score: score + (isTaken ? 10 : 0), name: name || slug });
    }
    scored.sort((a, b) => a.score - b.score || a.name.localeCompare(b.name, 'he'));
    const rows = scored.slice(0, MAX_ROWS).map(s => s.row);
    return { rows, hidden: scored.length - rows.length };
  }

  allTaken(v: ChannelPickerView): boolean {
    return v.rows.length > 0 && v.hidden === 0 && v.rows.every(r => r.taken);
  }

  private pickable(): ChannelPickerRow[] {
    return this.view().rows.filter(r => !r.taken);
  }

  /** Keeps the highlight on a pickable row, or the first one. */
  private syncActive() {
    const rows = this.pickable();
    if (!rows.some(r => r.channel.slug === this.activeSlug)) {
      this.activeSlug = rows.length ? rows[0].channel.slug : null;
    }
  }

  private announceCount() {
    if (this.directory.state !== 'ready') return;
    const n = this.pickable().length;
    this.announcement = n === 0 ? 'לא נמצאו ערוצים' : n === 1 ? 'נמצא ערוץ אחד' : `נמצאו ${n} ערוצים`;
  }

  openPanel() {
    if (this.manual) return;
    if (!this.open) {
      this.open = true;
      this.announceCount();
    }
    this.syncActive();
  }

  close() {
    this.open = false;
    this.activeSlug = null;
  }

  /** Focus left the picker (not into its own panel): close it. */
  onFocusOut(event: FocusEvent) {
    const next = event.relatedTarget as Node | null;
    const host = event.currentTarget as HTMLElement;
    if (!next || !host.contains(next)) this.close();
  }

  onInput(value: string) {
    this.query = value;
    if (this.manual) return;
    this.open = true;
    this.activeSlug = null;
    this.syncActive();
    this.announceCount();
  }

  onKeydown(event: KeyboardEvent) {
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        if (this.manual) return;
        event.preventDefault();
        if (!this.open) {
          this.openPanel();
          return;
        }
        this.move(event.key === 'ArrowDown' ? 1 : -1);
        return;
      }
      case 'Enter':
        // Never submit the settings form from this field.
        event.preventDefault();
        if (this.manual) this.addManual();
        else if (this.open && this.activeSlug) this.pickSlug(this.activeSlug);
        else this.openPanel();
        return;
      case 'Escape':
        if (this.open) {
          event.preventDefault();
          event.stopPropagation();
          this.close();
        } else if (this.query) {
          event.preventDefault();
          this.query = '';
        }
        return;
      case 'Tab':
        this.close();
        return;
    }
  }

  private move(delta: number) {
    const rows = this.pickable();
    if (!rows.length) return;
    const i = rows.findIndex(r => r.channel.slug === this.activeSlug);
    const next = i < 0 ? (delta > 0 ? 0 : rows.length - 1) : (i + delta + rows.length) % rows.length;
    this.activeSlug = rows[next].channel.slug;
    const el = document.getElementById(this.optionId(this.activeSlug));
    el?.scrollIntoView({ block: 'nearest' });
  }

  hover(row: ChannelPickerRow) {
    if (!row.taken) this.activeSlug = row.channel.slug;
  }

  choose(row: ChannelPickerRow) {
    if (!row.taken) this.pickSlug(row.channel.slug);
  }

  private pickSlug(slug: string) {
    if (this.selected.includes(slug)) return;
    const channel = this.directory.channels.find(c => c.slug === slug);
    this.picked.emit(slug);
    this.announcement = `הערוץ ${channel?.name || slug} נוסף לרשימה`;
    this.query = '';
    this.close();
  }

  addManual() {
    const slug = this.query.trim();
    if (!slug) return;
    if (!this.selected.includes(slug)) {
      this.picked.emit(slug);
      this.announcement = `הערוץ ${slug} נוסף לרשימה`;
    }
    this.query = '';
  }

  retry() {
    this.refreshList();
  }
}
