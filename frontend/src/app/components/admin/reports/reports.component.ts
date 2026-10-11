import { Component, EventEmitter, Input, OnChanges, OnInit, Output, SimpleChanges } from '@angular/core';
import { RouterLink } from '@angular/router';
import { NbButtonModule, NbCardModule, NbIconModule, NbSpinnerModule, NbToastrService, NbTooltipModule } from "@nebular/theme";
import { firstValueFrom } from 'rxjs';
import { AdminService } from '../../../services/admin.service';
import { Reports, Report } from '../../../models/report.model';
import { SlugService } from '../../../services/slug.service';
import { ConfirmService } from '../../../services/confirm.service';
import { MessageTimePipe } from "../../../pipes/message-time.pipe";

/**
 * "דיווחים": what readers flagged. A report is closed (handled) or reopened
 * here; the message itself can be opened in the feed or deleted from here,
 * the same delete the feed's own button performs.
 */
@Component({
  selector: 'app-reports',
  imports: [
    RouterLink,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbSpinnerModule,
    NbTooltipModule,
    MessageTimePipe,
  ],
  templateUrl: './reports.component.html',
  styleUrl: './reports.component.scss'
})
export class ReportsComponent implements OnInit, OnChanges {

  reports: Reports = [];
  @Input() status: 'open' | 'closed' | 'all' = 'open';
  /** The reports endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  loading = true;
  loadFailed = false;
  /** Report ids with a request in flight, so a button cannot be tapped twice. */
  busy = new Set<number>();
  /** Messages deleted from this screen, so their cards say so. */
  deletedMessages = new Set<number>();

  readonly filters = [
    { id: 'reports', status: 'open', title: 'פתוחים', icon: 'alert-triangle-outline' },
    { id: 'reports-closed', status: 'closed', title: 'טופלו', icon: 'checkmark-circle-2-outline' },
    { id: 'reports-all', status: 'all', title: 'כל הדיווחים', icon: 'list-outline' },
  ] as const;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private slugService: SlugService,
    private confirm: ConfirmService,
  ) { }

  get slug(): string {
    return this.slugService.slug;
  }

  get emptyText(): string {
    switch (this.status) {
      case 'open': return 'אין דיווחים פתוחים — הכול טופל.';
      case 'closed': return 'עדיין לא טופל אף דיווח.';
      default: return 'אף קורא עדיין לא דיווח על הודעה.';
    }
  }

  ngOnInit(): void {
    this.load();
  }

  ngOnChanges(changes: SimpleChanges): void {
    // The manage page keeps one instance alive across the three report tabs,
    // so a filter change arrives as an input change, not a fresh component.
    if (changes['status'] && !changes['status'].firstChange) this.load();
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.adminService.getReports(this.status).then(reports => {
      this.reports = reports ?? [];
    }).catch((err) => {
      this.loadFailed = true;
      if (err?.status === 403 || err?.status === 401) {
        this.accessDenied.emit();
        return;
      }
      this.toastrService.danger('', 'לא הצלחנו לטעון את הדיווחים — נסו שוב');
    }).finally(() => this.loading = false);
  }

  toggleReport(report: Report) {
    if (this.busy.has(report.id)) return;
    // The flip used to be applied before the request and never undone, so a
    // 404/500 left the button reading "reopen" for a report the server still
    // has open. Send the intended state and apply it only once it is stored.
    const updated: Report = { ...report, closed: !report.closed, updatedAt: new Date() };
    this.busy.add(report.id);
    this.adminService.setReports(updated).then(() => {
      this.toastrService.success('', updated.closed ? 'הדיווח סומן כטופל' : 'הדיווח נפתח מחדש');
      if (this.status === 'all') {
        const i = this.reports.findIndex(r => r.id === report.id);
        if (i >= 0) this.reports[i] = updated;
      } else {
        this.reports = this.reports.filter(r => r.id !== report.id);
      }
    }).catch((err: any) => this.toastrService.danger('',
      err?.status === 404 ? 'הדיווח כבר לא קיים' : 'עדכון הדיווח נכשל — נסו שוב'))
      .finally(() => this.busy.delete(report.id));
  }

  /** The reported message, in the feed, in a new tab — the report stays open here. */
  viewReport(messageId: number) {
    window.open(`${window.location.origin}/channel/${this.slugService.slug}#${messageId}`, '_blank', 'noopener');
  }

  async deleteMessage(report: Report) {
    if (this.busy.has(report.id) || this.deletedMessages.has(report.messageId)) return;
    const ok = await this.confirm.ask({
      title: 'למחוק את ההודעה שדווחה?',
      message: 'ההודעה תוסתר מכל הקוראים. מי שרשאי לערוך עדיין יוכל לשחזר אותה מתוך הערוץ. הדיווח עצמו נשאר — סגרו אותו בנפרד.',
      confirmLabel: 'מחיקת ההודעה',
      status: 'danger',
    });
    if (!ok) return;
    this.busy.add(report.id);
    try {
      await firstValueFrom(this.adminService.deleteMessage(report.messageId));
      this.deletedMessages.add(report.messageId);
      this.toastrService.success('', 'ההודעה נמחקה');
    } catch (err: any) {
      this.toastrService.danger('', err?.status === 404
        ? 'ההודעה כבר לא קיימת'
        : err?.status === 403 || err?.status === 401
          ? 'אין לכם הרשאה למחוק את ההודעה הזו'
          : 'מחיקת ההודעה נכשלה — נסו שוב');
    } finally {
      this.busy.delete(report.id);
    }
  }
}
