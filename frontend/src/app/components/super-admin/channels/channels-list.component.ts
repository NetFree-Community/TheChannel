import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
  NbToastrService,
  NbTooltipModule,
} from '@nebular/theme';
import { SuperAdminService, ChannelData } from '../../../services/super-admin.service';
import { SLUG_PATTERN } from '../../../services/channel.service';
import { ConfirmService } from '../../../services/confirm.service';
import { ShareService } from '../../../services/share.service';

type StatusFilter = 'all' | 'active' | 'disabled';

@Component({
  selector: 'app-channels-list',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbFormFieldModule,
    NbTooltipModule,
  ],
  templateUrl: './channels-list.component.html',
  styleUrl: './channels-list.component.scss',
})
export class ChannelsListComponent implements OnInit {
  @Output() editFeatures = new EventEmitter<ChannelData>();
  @Output() manageUsers = new EventEmitter<ChannelData>();
  @Output() manageStorage = new EventEmitter<ChannelData>();

  channels: ChannelData[] = [];
  loading = true;
  loadFailed = false;

  query = '';
  statusFilter: StatusFilter = 'all';
  readonly statusFilters: { value: StatusFilter; label: string }[] = [
    { value: 'all', label: 'הכל' },
    { value: 'active', label: 'פעילים' },
    { value: 'disabled', label: 'מושבתים' },
  ];

  showCreateForm = false;
  newSlug = '';
  newName = '';
  newOwnerEmail = '';
  creating = false;

  // Slugs whose DELETE is in flight: the button stays clickable otherwise, and
  // a second click sent another DELETE that answered 404 once the first had
  // finished — "deleted" and "failed to delete" toasts for the same channel.
  deleting = new Set<string>();
  // Same guard for the kill switch, which is two requests (read, then write).
  toggling = new Set<string>();

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
    private share: ShareService,
  ) {}

  ngOnInit(): void {
    this.loadChannels();
  }

  loadChannels() {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getChannels()
      .then(channels => this.channels = channels || [])
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'רשימת הערוצים לא נטענה');
      })
      .finally(() => this.loading = false);
  }

  /** The rows that match the search box and the status filter. */
  get filtered(): ChannelData[] {
    const q = this.query.trim().toLowerCase();
    return this.channels.filter(c => {
      if (this.statusFilter === 'active' && c.features?.disabled) return false;
      if (this.statusFilter === 'disabled' && !c.features?.disabled) return false;
      if (!q) return true;
      return (c.name || '').toLowerCase().includes(q)
        || (c.slug || '').toLowerCase().includes(q)
        || (c.ownerEmail || '').toLowerCase().includes(q);
    });
  }

  get disabledCount(): number {
    return this.channels.filter(c => c.features?.disabled).length;
  }

  get hasFilter(): boolean {
    return !!this.query.trim() || this.statusFilter !== 'all';
  }

  clearFilter() {
    this.query = '';
    this.statusFilter = 'all';
  }

  channelUrl(slug: string): string {
    return this.share.channelUrl(slug);
  }

  /** First letter of the name, for a channel without a logo. */
  initial(channel: ChannelData): string {
    const name = (channel.name || channel.slug || '').trim();
    return name ? name[0].toUpperCase() : '?';
  }

  openCreate() {
    this.showCreateForm = true;
  }

  /** Live preview of what the slug will be after normalisation. */
  get normalizedSlug(): string {
    return this.normalizeSlug(this.newSlug);
  }

  private normalizeSlug(raw: string): string {
    return raw.trim().toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '');
  }

  createChannel() {
    // The same normalisation the self-service form applies as the user types:
    // the server's slug regex is lowercase-only, so " My Channel" was refused
    // with an English 400 while the operator saw nothing wrong with it.
    this.newSlug = this.normalizeSlug(this.newSlug);
    this.newName = this.newName.trim();
    this.newOwnerEmail = this.newOwnerEmail.trim();
    if (!this.newSlug || !this.newName || !this.newOwnerEmail) {
      this.toastr.warning('', 'יש למלא שם, כתובת ערוץ ואימייל של הבעלים');
      return;
    }
    if (!SLUG_PATTERN.test(this.newSlug)) {
      this.toastr.warning('', 'כתובת הערוץ לא תקינה — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים');
      return;
    }
    this.creating = true;
    this.superAdminService.createChannel(this.newSlug, this.newName, this.newOwnerEmail)
      .then(channel => {
        this.channels.push(channel);
        this.cancelCreate();
        this.clearFilter();
        this.toastr.success('', `הערוץ "${channel.name}" נוצר`);
      })
      .catch((err) => this.toastr.danger('', this.createErrorText(err)))
      .finally(() => this.creating = false);
  }

  /**
   * The server answers failures as plain English text (http.Error). This path
   * says "Invalid slug…", "Slug is reserved", "Invalid owner email", "Channel
   * already exists"; the self-service path it mirrors says "invalid slug",
   * "slug is reserved", "slug already taken", "another channel is already
   * being created". Matched case-insensitively, never shown as-is.
   */
  private createErrorText(err: any): string {
    const text = (typeof err?.error === 'string' ? err.error : '').toLowerCase();
    switch (err?.status) {
      case 409:
        return text.includes('already being created')
          ? 'יצירת ערוץ אחר עדיין בתהליך, נסו שוב בעוד רגע'
          : 'כתובת הערוץ כבר תפוסה, בחרו כתובת אחרת';
      case 400:
        if (text.includes('reserved')) return 'כתובת הערוץ שמורה למערכת, בחרו כתובת אחרת';
        if (text.includes('slug')) return 'כתובת הערוץ לא תקינה — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים';
        if (text.includes('email')) return 'כתובת המייל של הבעלים אינה תקינה';
        if (text.includes('too long')) return 'שם הערוץ ארוך מדי (עד 80 תווים)';
        if (text.includes('name is required')) return 'יש להזין שם לערוץ';
        return 'הפרטים שהוזנו אינם תקינים';
      default:
        return 'יצירת הערוץ לא הצליחה, נסו שוב';
    }
  }

  async deleteChannel(channel: ChannelData) {
    const slug = channel.slug;
    if (this.deleting.has(slug)) return;
    // Say what is actually destroyed — the server wipes the whole tenant.
    const ok = await this.confirm.ask({
      title: `למחוק את הערוץ "${channel.name || slug}"?`,
      message: `כל ההודעות, הקבצים וההרשאות של הערוץ (${slug}) יימחקו לצמיתות. אי אפשר לשחזר אותם.`,
      status: 'danger',
      confirmLabel: 'מחיקה לצמיתות',
    });
    if (!ok) return;
    this.deleting.add(slug);
    this.superAdminService.deleteChannel(slug)
      .then(() => {
        this.channels = this.channels.filter(c => c.slug !== slug);
        this.toastr.success('', `הערוץ "${channel.name || slug}" נמחק`);
      })
      .catch((err) => this.toastr.danger('', this.deleteErrorText(err)))
      .finally(() => this.deleting.delete(slug));
  }

  private deleteErrorText(err: any): string {
    switch (err?.status) {
      case 503: return 'הערוץ עסוק כרגע (פרסום מתוזמן בעיצומו), נסו שוב בעוד רגע';
      case 404: return 'הערוץ כבר לא קיים — רעננו את הרשימה';
      default: return 'מחיקת הערוץ לא הצליחה, נסו שוב';
    }
  }

  /** The kill switch, with a confirmation that names the channel. */
  async toggleDisabled(channel: ChannelData) {
    const slug = channel.slug;
    if (this.toggling.has(slug)) return;
    const disable = !channel.features?.disabled;
    const name = channel.name || slug;
    const ok = await this.confirm.ask(disable
      ? {
        title: `להשבית את הערוץ "${name}"?`,
        message: 'הערוץ ייסגר מיד לכל הקוראים והכותבים, כולל הבעלים, ובמקומו יוצג המסך "הערוץ מושבת". אפשר להפעיל אותו מחדש מכאן בכל רגע.',
        status: 'warning',
        icon: 'slash-outline',
        confirmLabel: 'השבתה',
      }
      : {
        title: `להפעיל מחדש את הערוץ "${name}"?`,
        message: 'הערוץ יחזור להיות זמין לכולם מיד.',
        status: 'primary',
        icon: 'checkmark-circle-2-outline',
        confirmLabel: 'הפעלה מחדש',
      });
    if (!ok) return;
    this.toggling.add(slug);
    this.superAdminService.setChannelDisabled(slug, disable)
      .then(features => {
        channel.features = features;
        this.toastr.success('', disable ? `הערוץ "${name}" הושבת` : `הערוץ "${name}" פעיל שוב`);
      })
      .catch((err) => this.toastr.danger('', err?.status === 404
        ? 'הערוץ כבר לא קיים — רעננו את הרשימה'
        : 'עדכון הערוץ לא הצליח, נסו שוב'))
      .finally(() => this.toggling.delete(slug));
  }

  cancelCreate() {
    this.showCreateForm = false;
    this.newSlug = '';
    this.newName = '';
    this.newOwnerEmail = '';
  }
}
