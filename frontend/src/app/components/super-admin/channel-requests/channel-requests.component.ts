import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbIconModule, NbToastrService, NbAlertModule, NbTooltipModule,
} from '@nebular/theme';
import { SuperAdminService, ChannelRequest } from '../../../services/super-admin.service';
import { SLUG_PATTERN } from '../../../services/channel.service';
import { ShareService } from '../../../services/share.service';

type RequestFilter = 'pending' | 'approved' | 'rejected' | 'all';

@Component({
  selector: 'app-channel-requests',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbAlertModule,
    NbTooltipModule,
  ],
  templateUrl: './channel-requests.component.html',
  styleUrl: './channel-requests.component.scss',
})
export class ChannelRequestsComponent implements OnInit {
  requests: ChannelRequest[] = [];
  loading = false;
  loadFailed = false;
  actionId = '';
  approveSlug = '';
  approveNotes = '';
  rejectNotes = '';
  approveMode = false;
  rejectMode = false;
  approving = false;
  rejecting = false;
  /**
   * Shown above the list, not under the request's row: the list reloads after
   * an approval and the row leaves the default "ממתינות" view, which took the
   * link the operator is supposed to send with it.
   */
  lastApproveResult: { reqId: string; name: string; channelSlug: string; ownerEmail: string } | null = null;

  filter: RequestFilter = 'pending';
  readonly filters: { value: RequestFilter; label: string }[] = [
    { value: 'pending', label: 'ממתינות' },
    { value: 'approved', label: 'אושרו' },
    { value: 'rejected', label: 'נדחו' },
    { value: 'all', label: 'הכל' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private share: ShareService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  /**
   * Since self-service creation became the primary path this screen is mostly
   * an audit log: `name` is the creator's display name and the channel's own
   * name only reaches the client inside `notes` ("self-service creation:
   * <name>"), which the table never rendered — nor the rejection reasons
   * typed here. The known prefix is translated; anything else is shown as is.
   */
  notesText(req: ChannelRequest): string {
    const prefix = 'self-service creation: ';
    if (req.notes?.startsWith(prefix)) {
      return 'יצירה עצמית: ' + req.notes.slice(prefix.length);
    }
    return req.notes || '';
  }

  visible(): ChannelRequest[] {
    return this.filter === 'all' ? this.requests : this.requests.filter(r => r.status === this.filter);
  }

  count(status: RequestFilter): number {
    return status === 'all' ? this.requests.length : this.requests.filter(r => r.status === status).length;
  }

  get emptyText(): string {
    switch (this.filter) {
      case 'pending': return 'אין בקשות שממתינות להחלטה.';
      case 'approved': return 'עדיין לא אושרה אף בקשה.';
      case 'rejected': return 'עדיין לא נדחתה אף בקשה.';
      default: return 'עדיין לא הגיעו בקשות. בקשה נרשמת כאן כשמישהו פותח ערוץ מהאתר או מבקש שנפתח לו אחד.';
    }
  }

  statusText(status: string): string {
    switch (status) {
      case 'pending': return 'ממתינה';
      case 'approved': return 'אושרה';
      case 'rejected': return 'נדחתה';
      default: return status;
    }
  }

  channelUrl(slug: string): string {
    return this.share.channelUrl(slug);
  }

  async copyLink(slug: string): Promise<void> {
    if (await this.share.copy(this.channelUrl(slug))) {
      this.toastr.success('', 'הקישור הועתק');
    }
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getChannelRequests()
      .then(reqs => { this.requests = reqs || []; })
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'הבקשות לא נטענו');
      })
      .finally(() => { this.loading = false; });
  }

  startApprove(req: ChannelRequest): void {
    this.lastApproveResult = null;
    this.approveMode = true;
    this.rejectMode = false;
    this.actionId = req.id;
    this.approveSlug = req.desiredSlug.toLowerCase().replace(/[^a-z0-9-]/g, '-');
    this.approveNotes = '';
  }

  startReject(req: ChannelRequest): void {
    this.lastApproveResult = null;
    this.rejectMode = true;
    this.approveMode = false;
    this.actionId = req.id;
    this.rejectNotes = '';
  }

  /** Live check of the final address, so the operator sees the problem before sending. */
  get approveSlugInvalid(): boolean {
    return !!this.approveSlug && !SLUG_PATTERN.test(this.approveSlug);
  }

  confirmApprove(): void {
    if (!this.approveSlug || this.approveSlugInvalid || this.approving) return;
    const id = this.actionId;
    const name = this.requests.find(r => r.id === id)?.name || '';
    this.approving = true;
    this.superAdminService.approveChannelRequest(id, this.approveSlug, this.approveNotes)
      .then(result => {
        this.lastApproveResult = { reqId: id, name, channelSlug: result.channelSlug, ownerEmail: result.ownerEmail };
        this.toastr.success('', 'הערוץ נוצר והבעלים מונה');
        // The inline form closes; the result card above the list carries the link.
        this.cancel();
        this.load();
      })
      .catch((err) => this.toastr.danger('', this.approveErrorText(err)))
      .finally(() => this.approving = false);
  }

  /**
   * The server answers failures as plain English text: "invalid slug format",
   * "slug is reserved", "slug already taken", "request already processed",
   * "Channel name is required/too long", "request not found". Never shown as-is.
   */
  private approveErrorText(err: any): string {
    const text = (typeof err?.error === 'string' ? err.error : '').toLowerCase();
    switch (err?.status) {
      case 409:
        return text.includes('processed')
          ? 'הבקשה כבר טופלה — רעננו את הרשימה'
          : 'כתובת הערוץ כבר תפוסה, בחרו כתובת אחרת';
      case 404:
        return 'הבקשה כבר לא קיימת — רעננו את הרשימה';
      case 400:
        if (text.includes('reserved')) return 'כתובת הערוץ שמורה למערכת, בחרו כתובת אחרת';
        if (text.includes('slug')) return 'כתובת הערוץ לא תקינה — 3 עד 50 תווים, אותיות אנגליות קטנות, ספרות ומקפים';
        if (text.includes('too long')) return 'שם הערוץ שבבקשה ארוך מדי (עד 80 תווים)';
        if (text.includes('name')) return 'בבקשה אין שם לערוץ, ולכן אי אפשר לאשר אותה';
        return 'הפרטים אינם תקינים';
      default:
        return 'אישור הבקשה לא הצליח, נסו שוב';
    }
  }

  confirmReject(): void {
    if (this.rejecting) return;
    const id = this.actionId;
    this.rejecting = true;
    this.superAdminService.rejectChannelRequest(id, this.rejectNotes)
      .then(() => {
        this.toastr.success('', 'הבקשה נדחתה');
        this.cancel();
        this.load();
      })
      .catch((err) => this.toastr.danger('', err?.status === 409
        ? 'הבקשה כבר טופלה — רעננו את הרשימה'
        : err?.status === 404
          ? 'הבקשה כבר לא קיימת — רעננו את הרשימה'
          : 'דחיית הבקשה לא הצליחה, נסו שוב'))
      .finally(() => this.rejecting = false);
  }

  dismissResult(): void {
    this.lastApproveResult = null;
  }

  cancel(): void {
    this.actionId = '';
    this.approveMode = false;
    this.rejectMode = false;
    this.approveSlug = '';
    this.approveNotes = '';
    this.rejectNotes = '';
  }
}
