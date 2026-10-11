import { Component, HostBinding, Input, OnInit, Optional } from '@angular/core';
import { DatePipe } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule, NbButtonModule, NbCardModule, NbDialogRef,
  NbIconModule, NbInputModule, NbSpinnerModule, NbToastrService, NbTooltipModule,
} from '@nebular/theme';
import { SupportService, SupportTicket } from '../../services/support.service';

/**
 * "Contact the operator" — the same box on the public landing page, inside a
 * channel's manage page and as a dialog from the channel header.
 *
 * The surfaces differ only in what the sender has to type: signed in, the
 * name and email come from the session (the server ignores them in the body
 * either way), so the form is just a subject and a message. Anonymous, the
 * email is required, because it is the only way the operator knows who asked.
 *
 * Existing threads are listed underneath so a reply is read in the same place
 * it was sent from. A signed-in user's threads come from the server by session
 * email; an anonymous sender's come from the tokens this browser kept. The
 * operator's replies are labelled "ניהול" and never carry a name.
 */
@Component({
  selector: 'app-support-box',
  standalone: true,
  imports: [
    DatePipe, FormsModule, NbCardModule, NbButtonModule, NbInputModule,
    NbIconModule, NbAlertModule, NbSpinnerModule, NbTooltipModule,
  ],
  templateUrl: './support-box.component.html',
  styleUrl: './support-box.component.scss',
})
export class SupportBoxComponent implements OnInit {
  @Input() title = 'פנייה למערכת';
  @Input() subtitle = '';
  /** Set when the box is shown inside a channel, so the operator sees which. */
  @Input() channelSlug = '';
  /** Drives which fields are asked for; the server decides identity regardless. */
  @Input() signedIn = false;
  /**
   * Set by the caller that opens the box as its own dialog (the header menu).
   * Not derived from the injected NbDialogRef: as a manage-page section there
   * is no dialog of its own to close, and a stray ref from an enclosing dialog
   * must not be closed by this button.
   */
  @Input() dialogMode = false;

  @HostBinding('class.support-dialog') get isDialog(): boolean { return this.dialogMode; }

  readonly subjectMax = 200;
  readonly bodyMax = 5000;

  subject = '';
  body = '';
  name = '';
  email = '';

  submitting = false;
  sent = false;
  error = '';

  tickets: SupportTicket[] = [];
  ticketsLoading = false;
  openId = '';
  replyBody = '';
  replying = false;

  constructor(
    private support: SupportService,
    private toastr: NbToastrService,
    @Optional() private dialogRef: NbDialogRef<SupportBoxComponent> | null,
  ) {}

  close(): void {
    if (this.dialogMode) this.dialogRef?.close();
  }

  ngOnInit(): void {
    this.loadTickets();
  }

  async submit(): Promise<void> {
    this.error = '';
    // A new attempt clears the previous outcome too, or the green "sent"
    // banner and a red validation error stood on screen together.
    this.sent = false;
    if (!this.subject.trim() || !this.body.trim()) {
      this.error = 'יש למלא נושא ותוכן.';
      return;
    }
    const email = this.email.trim();
    if (!this.signedIn && !email) {
      this.error = 'יש להזין כתובת אימייל כדי שנוכל לחזור אליכם.';
      return;
    }
    // Same shape check as the server's looksLikeEmail, so a typo is caught
    // here instead of by a 400 round trip.
    if (!this.signedIn && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      this.error = 'יש להזין כתובת אימייל תקינה.';
      return;
    }

    this.submitting = true;
    try {
      await this.support.createTicket({
        subject: this.subject.trim(),
        body: this.body.trim(),
        name: this.name.trim(),
        email: this.email.trim(),
        channelSlug: this.channelSlug,
      });
      this.sent = true;
      this.subject = '';
      this.body = '';
      await this.loadTickets();
    } catch (err: any) {
      this.error = this.errorText(err);
    } finally {
      this.submitting = false;
    }
  }

  toggle(id: string): void {
    this.openId = this.openId === id ? '' : id;
    this.replyBody = '';
  }

  async sendReply(t: SupportTicket): Promise<void> {
    const body = this.replyBody.trim();
    if (!body) return;
    this.replying = true;
    try {
      const updated = await this.support.reply(t.id, body, this.tokenFor(t.id));
      // Replace in place so the open thread updates without a full reload.
      const i = this.tickets.findIndex(x => x.id === t.id);
      if (i >= 0) this.tickets[i] = updated;
      this.replyBody = '';
    } catch (err: any) {
      this.toastr.danger('', this.errorText(err));
    } finally {
      this.replying = false;
    }
  }

  statusText(s: string): string {
    switch (s) {
      case 'open': return 'ממתין למענה';
      case 'answered': return 'נענתה';
      case 'closed': return 'נסגרה';
      default: return s;
    }
  }

  statusIcon(s: string): string {
    switch (s) {
      case 'open': return 'clock-outline';
      case 'answered': return 'message-circle-outline';
      case 'closed': return 'checkmark-circle-2-outline';
      default: return 'email-outline';
    }
  }

  private tokenFor(id: string): string | undefined {
    return this.support.anonTickets().find(t => t.id === id)?.token;
  }

  /**
   * Signed in, the server finds the threads by session email. Anonymous, they
   * are fetched one by one with the tokens this browser kept — a ticket opened
   * elsewhere is simply not listed, which is the point of the token.
   */
  private async loadTickets(): Promise<void> {
    this.ticketsLoading = true;
    try {
      if (this.signedIn) {
        try {
          this.tickets = await this.support.myTickets();
        } catch {
          this.tickets = [];
        }
        return;
      }

      const stored = this.support.anonTickets();
      const loaded = await Promise.all(
        stored.map(s => this.support.getTicket(s.id, s.token).catch(() => null)),
      );
      // A ticket that has expired server-side drops out rather than erroring.
      this.tickets = loaded.filter((t): t is SupportTicket => t !== null);
    } finally {
      this.ticketsLoading = false;
    }
  }

  private errorText(err: any): string {
    // The server's bodies are English one-liners meant for its logs; they are
    // only ever matched on here, never shown in the Hebrew form.
    const text = typeof err?.error === 'string' && err.error ? err.error : '';
    switch (err?.status) {
      case 429: return 'נשלחו יותר מדי פניות בזמן קצר. נסו שוב בעוד כמה דקות.';
      case 400:
        // A signed-in box hides the email field, so once the session has lapsed
        // (cookie expired, logged out in another tab) the server's "email
        // required" cannot be satisfied from here — only a fresh sign-in can.
        if (text.includes('email')) {
          return this.signedIn
            ? 'ההתחברות פגה. יש להתחבר מחדש ולשלוח את הפנייה שוב.'
            : 'יש להזין כתובת אימייל תקינה.';
        }
        if (text.includes('subject and body')) return 'יש למלא נושא ותוכן.';
        if (text.includes('message body')) return 'יש להזין תוכן להודעה.';
        return 'הפרטים שהוזנו אינם תקינים.';
      case 401:
        return this.signedIn
          ? 'ההתחברות פגה. יש להתחבר מחדש ולנסות שוב.'
          : 'אין הרשאה לבצע פעולה זו.';
      case 409:
        // Both a closed ticket and a full thread answer 409; the body tells
        // them apart. A full thread used to read as "closed", which the
        // requester took for the operator closing it.
        return text.includes('message limit')
          ? 'הפנייה הגיעה למספר ההודעות המרבי. לשאלה נוספת, פתחו פנייה חדשה.'
          : 'הפנייה נסגרה ולא ניתן להוסיף לה הודעות.';
      case 404: return 'הפנייה לא נמצאה.';
      default: return 'שגיאה בשליחה. נסו שוב.';
    }
  }
}
