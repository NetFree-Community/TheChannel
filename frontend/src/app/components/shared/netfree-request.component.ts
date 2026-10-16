import { Component, ElementRef, Input, ViewChild } from '@angular/core';
import { NbButtonModule, NbIconModule } from '@nebular/theme';
import { ShareService } from '../../services/share.service';

/** NetFree's "new ticket" page; it needs a NetFree account. */
export const NETFREE_TICKET_URL = 'https://netfree.link/app/#/tickets/new';

/**
 * "פתיחת הערוץ בנטפרי": a ready request for NetFree, filled with the channel's
 * address and with an answer to every question NetFree asks before opening a
 * content site (ownership, audience, whether readers can write, the policy
 * commitment, the personal details and the selfie video). The owner copies
 * it, fills the blanks and pastes it into a NetFree ticket. Shown on the
 * create-success card and on the channel's "פרטי הערוץ" screen.
 */
@Component({
  selector: 'app-netfree-request',
  standalone: true,
  imports: [NbButtonModule, NbIconModule],
  templateUrl: './netfree-request.component.html',
  styleUrl: './netfree-request.component.scss',
})
export class NetfreeRequestComponent {
  @Input() slug = '';
  @Input() channelName = '';
  @Input() channelDescription = '';
  /** Starts with the text hidden (the create-success card is busy enough). */
  @Input() collapsed = false;

  @ViewChild('requestText') private requestText?: ElementRef<HTMLTextAreaElement>;

  readonly ticketUrl = NETFREE_TICKET_URL;
  copied = false;
  copyFailed = false;
  private copiedTimer?: ReturnType<typeof setTimeout>;

  constructor(private share: ShareService) {}

  get channelUrl(): string {
    return this.share.channelUrl(this.slug);
  }

  get text(): string {
    return netfreeRequestText({
      url: this.channelUrl,
      host: window.location.host,
      name: this.channelName,
      description: this.channelDescription,
    });
  }

  toggle(): void {
    this.collapsed = !this.collapsed;
  }

  async copy(): Promise<void> {
    clearTimeout(this.copiedTimer);
    // quiet: the browser's prompt() fallback is one line and would flatten
    // the request; on failure the text is shown selected instead.
    this.copied = await this.share.copy(this.text, { quiet: true });
    this.copyFailed = !this.copied;
    if (this.copied) {
      this.copiedTimer = setTimeout(() => (this.copied = false), 2500);
    } else {
      this.collapsed = false;
      setTimeout(() => this.requestText?.nativeElement.select());
    }
  }
}

/** The request itself. Blanks are left as ______ for the owner to fill. */
export function netfreeRequestText(c: { url: string; host: string; name: string; description: string }): string {
  const blank = '______';
  const name = oneLine(c.name) || blank;
  const about = oneLine(c.description) || blank;
  return [
    'שלום רב,',
    'אבקש לפתוח בנטפרי את ערוץ העדכונים שלי, בכתובת:',
    c.url,
    '',
    `שם הערוץ: ${name}`,
    `במה הערוץ עוסק: ${about}`,
    '',
    'תשובות לשאלות שלכם:',
    '',
    `1. בעלות ושותפים: פתחתי את הערוץ ואני מנהל/ת אותו. הערוץ הוא דף באתר הערוצים ${c.host}, ואני אחראי/ת על התוכן של הערוץ הזה בלבד. מנהלים נוספים בערוץ: ${blank} (אם יש, תיאמתי איתם ואני מוסמך/ת להתחייב גם בשמם).`,
    '',
    '2. הוכחת בעלות: אוכל לפרסם בערוץ כל הודעה, תמונה או קובץ שתבקשו.',
    '',
    `3. קהל היעד: ${blank}`,
    '',
    '4. האם הציבור יכול לכתוב תוכן או להגיב? לא. רק מנהלי הערוץ, שאני מוסיף/ה בעצמי, מפרסמים הודעות. הקוראים יכולים רק לקרוא, ולכל היותר לסמן אימוג\'י על הודעה. אין בערוץ תגובות כתובות ואין פורום.',
    '',
    `5. מספר קוראים צפוי: ${blank}`,
    '',
    '6. קראתי את מדיניות נטפרי ואת המדיניות לאתרי תוכן, ואני מתחייב/ת שהערוץ לא יחרוג מהכללים. ידוע לי שאי עמידה בכללים עלולה לגרור חסימה של הערוץ לתקופה של 3 חודשים.',
    '',
    'פרטים אישיים:',
    'שם פרטי ומשפחה: ',
    'טלפון: ',
    'גיל: ',
    'מצב משפחתי: ',
    'מקום לימודים (ישיבה גדולה/סמינר): ',
    '',
    'מצורף וידאו סלפי שבו אני מתחייב/ת לעמוד בכללים.',
    'אם יפורסמו בערוץ תמונות או קבצים, אבקש לפתוח גם אותם.',
    '',
    'תודה רבה!',
  ].join('\n');
}

function oneLine(s: string | undefined | null): string {
  return (s || '').split(/\s+/).filter(Boolean).join(' ');
}
