import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbIconModule,
  NbInputModule, NbToastrService,
} from '@nebular/theme';
import {
  SupportService, SupportStatus, SupportTicket,
} from '../../../services/support.service';

/**
 * The operator's inbox: every ticket from every channel, plus the ones sent
 * from the public landing page by people with no account at all.
 *
 * Ordered by last activity, so a thread the requester just added to comes back
 * to the top rather than staying buried at its creation time.
 */
@Component({
  selector: 'app-support-inbox',
  standalone: true,
  imports: [
    CommonModule, FormsModule, NbCardModule, NbButtonModule,
    NbInputModule, NbIconModule,
  ],
  templateUrl: './support-inbox.component.html',
  styleUrl: './support-inbox.component.scss',
})
export class SupportInboxComponent implements OnInit {
  tickets: SupportTicket[] = [];
  loading = true;
  loadFailed = false;
  busy = false;
  openId = '';
  replyBody = '';

  filter: 'all' | SupportStatus = 'open';
  readonly filters: { value: 'all' | SupportStatus; label: string }[] = [
    { value: 'open', label: 'ממתינות למענה' },
    { value: 'answered', label: 'נענו' },
    { value: 'closed', label: 'סגורות' },
    { value: 'all', label: 'הכל' },
  ];

  constructor(
    private support: SupportService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.reload();
  }

  async reload(): Promise<void> {
    this.loading = true;
    this.loadFailed = false;
    try {
      this.tickets = await this.support.adminList();
    } catch {
      this.tickets = [];
      this.loadFailed = true;
      this.toastr.danger('', 'הפניות לא נטענו');
    } finally {
      this.loading = false;
    }
  }

  visible(): SupportTicket[] {
    return this.filter === 'all'
      ? this.tickets
      : this.tickets.filter(t => t.status === this.filter);
  }

  count(status: 'all' | SupportStatus): number {
    return status === 'all' ? this.tickets.length : this.tickets.filter(t => t.status === status).length;
  }

  get openCount(): number {
    return this.count('open');
  }

  /** The empty-state sentence for the active filter. */
  get emptyText(): string {
    switch (this.filter) {
      case 'open': return 'אין פניות שממתינות למענה. כל הכבוד.';
      case 'answered': return 'אין פניות שנענו ועדיין פתוחות.';
      case 'closed': return 'אין פניות סגורות.';
      default: return 'עדיין לא הגיעו פניות. הן נשלחות מהאתר ומהמסך "פנייה לתמיכה" של כל ערוץ.';
    }
  }

  toggle(id: string): void {
    this.openId = this.openId === id ? '' : id;
    this.replyBody = '';
  }

  async reply(t: SupportTicket): Promise<void> {
    const body = this.replyBody.trim();
    if (!body) {
      this.toastr.warning('', 'כתבו תשובה לפני השליחה');
      return;
    }
    this.busy = true;
    try {
      this.replace(await this.support.adminReply(t.id, body));
      this.replyBody = '';
      this.toastr.success('', 'התשובה נשלחה לפונה');
    } catch (err: any) {
      this.toastr.danger('', this.updateErrorText(err, 'שליחת התשובה לא הצליחה, נסו שוב'));
    } finally {
      this.busy = false;
    }
  }

  async setStatus(t: SupportTicket, status: SupportStatus): Promise<void> {
    this.busy = true;
    try {
      this.replace(await this.support.adminSetStatus(t.id, status));
      this.toastr.success('', status === 'closed' ? 'הפנייה נסגרה' : 'הפנייה נפתחה מחדש');
    } catch (err: any) {
      this.toastr.danger('', this.updateErrorText(err, 'עדכון הפנייה לא הצליח, נסו שוב'));
    } finally {
      this.busy = false;
    }
  }

  /**
   * The server's plain-text refusals: 404 for a ticket that is gone, 409
   * "this thread has reached its message limit". Never shown as-is.
   */
  private updateErrorText(err: any, fallback: string): string {
    const text = typeof err?.error === 'string' ? err.error.toLowerCase() : '';
    if (err?.status === 404) return 'הפנייה כבר לא קיימת — רעננו את הרשימה';
    if (err?.status === 409 && text.includes('limit')) return 'השרשור הגיע למגבלת ההודעות שלו. סגרו אותו ובקשו מהפונה לפתוח פנייה חדשה.';
    if (err?.status === 400) return 'התשובה ריקה או ארוכה מדי (עד 5,000 תווים)';
    return fallback;
  }

  statusText(s: string): string {
    switch (s) {
      case 'open': return 'ממתינה למענה';
      case 'answered': return 'נענתה';
      case 'closed': return 'סגורה';
      default: return s;
    }
  }

  /** Swap the server's updated copy in without reloading the whole inbox. */
  private replace(updated: SupportTicket): void {
    const i = this.tickets.findIndex(x => x.id === updated.id);
    if (i >= 0) this.tickets[i] = updated;
  }
}
