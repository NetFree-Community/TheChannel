import { Component, HostListener, OnInit, ViewEncapsulation } from '@angular/core';
import { ChatService } from '../../../services/chat.service';
import {
  NbToastrService, NbCardModule, NbButtonModule, NbIconModule, NbTagComponent, NbTagModule, NbSpinnerModule,
} from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { PickerModule } from '@ctrl/ngx-emoji-mart';

@Component({
  selector: 'app-emojis',
  imports: [
    NbCardModule,
    NbButtonModule,
    PickerModule,
    NbIconModule,
    NbTagModule,
    NbSpinnerModule,
  ],
  templateUrl: './emojis.component.html',
  styleUrl: './emojis.component.scss',
  // Global on purpose: the stylesheet is the emoji-mart picker's, whose
  // elements live in a child component that emulated encapsulation would miss.
  encapsulation: ViewEncapsulation.None,
})
export class EmojisComponent implements OnInit {
  emojis: string[] | undefined = [];
  loading = true;
  saving = false;
  private snapshot = '';

  /** Emojis per row in the picker — fewer on a narrow phone so it never overflows. */
  perLine = 9;

  /** The picker's own labels, in Hebrew. */
  readonly pickerI18n = {
    search: 'חיפוש אימוג\'י',
    emojilist: 'רשימת אימוג\'ים',
    notfound: 'לא נמצא אימוג\'י',
    clear: 'ניקוי',
    categories: {
      search: 'תוצאות חיפוש',
      recent: 'בשימוש לאחרונה',
      people: 'סמיילים ואנשים',
      nature: 'חיות וטבע',
      foods: 'אוכל ושתייה',
      activity: 'פעילות',
      places: 'נסיעות ומקומות',
      objects: 'חפצים',
      symbols: 'סמלים',
      flags: 'דגלים',
      custom: 'מותאם אישית',
    },
    skintones: {
      1: 'גוון עור ברירת מחדל',
      2: 'גוון עור בהיר',
      3: 'גוון עור בהיר-בינוני',
      4: 'גוון עור בינוני',
      5: 'גוון עור בינוני-כהה',
      6: 'גוון עור כהה',
    },
  };

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  get dirty(): boolean {
    return !!this.emojis && JSON.stringify(this.emojis) !== this.snapshot;
  }

  @HostListener('window:resize')
  onResize() {
    this.updatePerLine();
  }

  ngOnInit(): void {
    this.updatePerLine();
    this.load();
  }

  load(): void {
    this.loading = true;
    this.chatService.getEmojisList(true)
      // Copied: the service resolves with its cached array, the same instance
      // every message's reaction picker holds, so push/splice on it showed
      // unsaved edits everywhere and kept them after closing without saving.
      .then(emojis => {
        this.emojis = [...emojis];
        this.snapshot = JSON.stringify(this.emojis);
      })
      .catch(() => {
        this.toastrService.danger('', 'לא הצלחנו לטעון את רשימת האימוג\'ים');
        this.emojis = undefined;
      })
      .finally(() => this.loading = false);
  }

  setEmojis() {
    // A failed load leaves emojis undefined; saving then would wipe the
    // channel's stored list. An intentionally emptied list ([]) still saves.
    if (!this.emojis) {
      this.toastrService.warning('', 'רשימת האימוג\'ים לא נטענה — לא ניתן לשמור');
      return;
    }

    this.saving = true;
    this.adminService.setEmojis(this.emojis)
      .then(() => {
        this.toastrService.success('', 'רשימת האימוג\'ים נשמרה');
        this.snapshot = JSON.stringify(this.emojis);
        // Nothing else refreshes the cache after a save; without this the
        // reaction pickers keep the pre-save list until the next channel load.
        this.chatService.getEmojisList(true).catch(() => null);
      })
      .catch((err) => {
        this.toastrService.danger('', err?.status === 403 || err?.status === 401
          ? 'אין לכם הרשאה לשנות את האימוג\'ים של הערוץ'
          : 'שמירת האימוג\'ים נכשלה — נסו שוב');
      })
      .finally(() => this.saving = false);
  }

  addEmoji(event: any) {
    const emoji = event.emoji.native;
    if (!this.emojis?.includes(emoji)) this.emojis?.push(emoji)
  }

  removeEmoji(emoji: number) {
    this.emojis?.splice(emoji, 1);
  }

  /** nb-tag-list hands back the tag; the chip's text is the emoji itself. */
  onTagRemove(tag: NbTagComponent) {
    const i = this.emojis?.indexOf(tag.text) ?? -1;
    if (i >= 0) this.removeEmoji(i);
  }

  private updatePerLine() {
    const width = typeof window === 'undefined' ? 1024 : window.innerWidth;
    // ~36px per cell plus the picker's own padding and the page gutters.
    this.perLine = Math.max(6, Math.min(9, Math.floor((width - 96) / 36)));
  }
}
