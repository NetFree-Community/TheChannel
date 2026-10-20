import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbIconModule, NbInputModule, NbSpinnerModule, NbToastrService, NbTooltipModule,
} from '@nebular/theme';
import { ChannelTag, ChatService } from '../../../services/chat.service';
import { AdminService } from '../../../services/admin.service';
import { ConfirmService } from '../../../services/confirm.service';
import { TAG_PALETTE, tagInk } from '../../../utils/tag-colors';

/** Mirrors the server's limits (backend/tags.go). */
const MAX_TAGS = 30;
const MAX_NAME = 24;

/**
 * The channel's message tags: managers add, rename, recolour, reorder and
 * remove them here. Writers then pick one (or none) per post in the
 * composer, and every reader can filter the feed by them.
 */
@Component({
  selector: 'app-tags',
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbSpinnerModule,
    NbTooltipModule,
  ],
  templateUrl: './tags.component.html',
  styleUrl: './tags.component.scss',
})
export class TagsComponent implements OnInit {
  readonly palette = TAG_PALETTE;
  readonly maxTags = MAX_TAGS;
  readonly maxName = MAX_NAME;
  readonly ink = tagInk;

  tags: ChannelTag[] | undefined = [];
  loading = true;
  saving = false;
  private snapshot = '';
  private loaded: ChannelTag[] = [];

  /** Index of the row whose colour palette is open, or -1. */
  colorOpen = -1;

  newName = '';
  newColor = TAG_PALETTE[0].color;

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private confirm: ConfirmService,
  ) { }

  get dirty(): boolean {
    return !!this.tags && JSON.stringify(this.tags) !== this.snapshot;
  }

  get canAdd(): boolean {
    return !!this.tags && this.tags.length < MAX_TAGS && !!this.newName.trim() && !this.nameTaken(this.newName);
  }

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.colorOpen = -1;
    this.chatService.getTags(true)
      // Copied: the service's array is the one the feed renders from.
      .then(tags => this.reset(tags))
      .catch(() => {
        this.toastrService.danger('', 'לא הצלחנו לטעון את התגיות');
        this.tags = undefined;
      })
      .finally(() => this.loading = false);
  }

  private reset(tags: ChannelTag[]) {
    this.tags = tags.map(t => ({ ...t }));
    this.loaded = tags.map(t => ({ ...t }));
    this.snapshot = JSON.stringify(this.tags);
    // The next suggested colour is one the channel does not use yet.
    const used = new Set(this.tags.map(t => t.color.toLowerCase()));
    this.newColor = (TAG_PALETTE.find(p => !used.has(p.color)) ?? TAG_PALETTE[0]).color;
  }

  nameTaken(name: string, exceptIndex = -1): boolean {
    const n = name.trim().toLowerCase();
    return !!this.tags?.some((t, i) => i !== exceptIndex && t.name.trim().toLowerCase() === n);
  }

  addTag(): void {
    if (!this.tags || !this.canAdd) return;
    this.tags.push({ id: '', name: this.newName.trim(), color: this.newColor });
    this.newName = '';
    const used = new Set(this.tags.map(t => t.color.toLowerCase()));
    this.newColor = TAG_PALETTE.find(p => !used.has(p.color))?.color ?? this.newColor;
  }

  removeTag(i: number): void {
    this.tags?.splice(i, 1);
    this.colorOpen = -1;
  }

  move(i: number, delta: number): void {
    if (!this.tags) return;
    const j = i + delta;
    if (j < 0 || j >= this.tags.length) return;
    [this.tags[i], this.tags[j]] = [this.tags[j], this.tags[i]];
    this.colorOpen = this.colorOpen === i ? j : this.colorOpen === j ? i : this.colorOpen;
  }

  toggleColor(i: number): void {
    this.colorOpen = this.colorOpen === i ? -1 : i;
  }

  setColor(i: number, color: string): void {
    if (this.tags?.[i]) this.tags[i].color = color.toLowerCase();
  }

  isPreset(color: string): boolean {
    return TAG_PALETTE.some(p => p.color === color.toLowerCase());
  }

  async save(): Promise<void> {
    if (!this.tags) return;
    if (this.tags.some(t => !t.name.trim())) {
      this.toastrService.warning('', 'לכל תגית צריך שם');
      return;
    }
    if (this.tags.some((t, i) => this.nameTaken(t.name, i))) {
      this.toastrService.warning('', 'יש שתי תגיות עם אותו שם');
      return;
    }
    const keep = new Set(this.tags.map(t => t.id).filter(Boolean));
    const removed = this.loaded.filter(t => !keep.has(t.id));
    if (removed.length) {
      const names = removed.map(t => `"${t.name}"`).join(', ');
      const ok = await this.confirm.ask({
        title: removed.length === 1 ? 'למחוק את התגית?' : `למחוק ${removed.length} תגיות?`,
        message: `${names} ${removed.length === 1 ? 'תוסר' : 'יוסרו'} מהערוץ ומכל ההודעות שתויגו בה. ההודעות עצמן נשארות.`,
        status: 'danger',
        confirmLabel: 'מחיקה ושמירה',
      });
      if (!ok) return;
    }

    this.saving = true;
    try {
      const res = await this.adminService.setTags(this.tags.map(t => ({ ...t, name: t.name.trim() })));
      this.toastrService.success('', 'התגיות נשמרו');
      // The server issues ids for new tags; keep its list as the new baseline.
      this.reset(res?.tags ?? this.tags);
      this.chatService.getTags(true).catch(() => null);
    } catch (err: any) {
      const msg = err?.error?.error;
      this.toastrService.danger('', err?.status === 403 || err?.status === 401
        ? 'אין לכם הרשאה לשנות את התגיות של הערוץ'
        : msg || 'שמירת התגיות נכשלה — נסו שוב');
    } finally {
      this.saving = false;
    }
  }
}
