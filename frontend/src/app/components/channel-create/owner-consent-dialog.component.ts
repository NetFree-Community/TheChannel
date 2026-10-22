import { Component, Input } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbButtonModule, NbCardModule, NbDialogRef, NbIconModule } from '@nebular/theme';
import { ConsentService } from '../../services/consent.service';

/**
 * The welcome card a new channel opener sees before the channel is created:
 * the terms of service and the privacy policy must be accepted (one required
 * checkbox), and the mailing list is a separate, optional, unticked checkbox.
 * The law wants marketing mail on an explicit opt-in that the service does not
 * depend on, so the two are never merged and "no" to mail never blocks.
 *
 * Closes with true once the answers are stored, false when the user backs out.
 */
@Component({
  selector: 'app-owner-consent-dialog',
  standalone: true,
  imports: [FormsModule, NbCardModule, NbButtonModule, NbIconModule],
  template: `
    <nb-card class="consent" role="dialog" aria-modal="true" aria-labelledby="consent-title">
      <nb-card-body class="consent__body">
        <div class="consent__badge" aria-hidden="true">
          <nb-icon icon="shield-outline"></nb-icon>
        </div>
        <h2 class="consent__title" id="consent-title">
          {{ name ? 'ברוכים הבאים, ' + name + '!' : 'ברוכים הבאים!' }}
        </h2>
        <p class="consent__lead">
          עוד רגע הערוץ שלכם באוויר. לפני שפותחים, צריך רק לאשר את הכללים של השירות.
        </p>

        <label class="consent__item" [class.consent__item--on]="terms" [class.consent__item--missing]="missing && !terms">
          <input type="checkbox" class="consent__check" name="terms" [(ngModel)]="terms"
                 (ngModelChange)="missing = false" aria-required="true">
          <span class="consent__box" aria-hidden="true"><nb-icon icon="checkmark-outline"></nb-icon></span>
          <span class="consent__text">
            קראתי ואני מסכים/ה ל<a href="/terms" target="_blank" rel="noopener">תנאי השימוש</a>
            ול<a href="/privacy" target="_blank" rel="noopener">מדיניות הפרטיות</a>
            <span class="consent__req">(חובה)</span>
          </span>
        </label>

        <label class="consent__item" [class.consent__item--on]="marketing">
          <input type="checkbox" class="consent__check" name="marketing" [(ngModel)]="marketing">
          <span class="consent__box" aria-hidden="true"><nb-icon icon="checkmark-outline"></nb-icon></span>
          <span class="consent__text">
            אשמח לקבל מכם במייל עדכונים, טיפים להצלחת הערוץ והצעות מהערוצים וממגנט.
            <span class="consent__sub">לא חובה. אפשר להסיר את עצמכם בכל עת בקישור שבכל מייל.</span>
          </span>
        </label>

        @if (missing) {
          <p class="consent__error" role="alert">כדי לפתוח ערוץ צריך לאשר את תנאי השימוש ומדיניות הפרטיות.</p>
        }
        @if (error) {
          <p class="consent__error" role="alert">{{ error }}</p>
        }

        <button nbButton status="primary" size="large" fullWidth type="button"
                class="consent__go" [disabled]="saving" (click)="confirm()">
          @if (saving) {
            <nb-icon aria-hidden="true" icon="loader-outline" class="spin"></nb-icon>
            שומרים…
          } @else {
            אישור והמשך לפתיחת הערוץ
          }
        </button>
        <button nbButton ghost status="basic" size="small" type="button"
                class="consent__later" [disabled]="saving" (click)="ref.close(false)">
          לא עכשיו
        </button>
      </nb-card-body>
    </nb-card>
  `,
  styles: [`
    .consent {
      width: min(94vw, 460px);
      margin: 0;
      border-radius: 1.25rem;
      overflow: hidden;
    }
    .consent__body {
      padding: 2rem 1.5rem 1.25rem;
      text-align: center;
      background:
        radial-gradient(120% 60% at 50% 0%, var(--color-primary-100) 0%, transparent 70%);
    }
    .consent__badge {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 3.5rem;
      height: 3.5rem;
      border-radius: 1rem;
      margin-bottom: 0.9rem;
      font-size: 1.75rem;
      color: #fff;
      background: linear-gradient(135deg, var(--color-primary-500), var(--color-primary-700));
      box-shadow: 0 8px 20px -8px var(--color-primary-500);
    }
    .consent__badge nb-icon { font-size: inherit; width: 1em; height: 1em; }
    .consent__title {
      margin: 0 0 0.4rem;
      font-size: 1.35rem;
      font-weight: 800;
      color: var(--text-basic-color);
    }
    .consent__lead {
      margin: 0 auto 1.25rem;
      max-width: 22rem;
      line-height: 1.6;
      color: var(--text-hint-color);
    }
    .consent__item {
      position: relative;
      display: flex;
      align-items: flex-start;
      gap: 0.75rem;
      padding: 0.85rem 0.9rem;
      margin-bottom: 0.6rem;
      text-align: start;
      border: 1.5px solid var(--border-basic-color-4);
      border-radius: 0.85rem;
      background: var(--background-basic-color-1);
      cursor: pointer;
      transition: border-color 0.15s, background-color 0.15s;
    }
    .consent__item:hover { border-color: var(--color-primary-300); }
    .consent__item--on {
      border-color: var(--color-primary-500);
      background: var(--color-primary-transparent-100);
    }
    .consent__item--missing { border-color: var(--color-danger-500); }
    .consent__check {
      position: absolute;
      opacity: 0;
      width: 1px;
      height: 1px;
    }
    .consent__box {
      flex: none;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 1.35rem;
      height: 1.35rem;
      margin-top: 0.1rem;
      border-radius: 0.4rem;
      border: 2px solid var(--border-basic-color-5);
      background: var(--background-basic-color-1);
      color: transparent;
      transition: background-color 0.15s, border-color 0.15s;
    }
    .consent__box nb-icon { font-size: 1rem; width: 1em; height: 1em; }
    .consent__check:checked + .consent__box {
      background: var(--color-primary-500);
      border-color: var(--color-primary-500);
      color: #fff;
    }
    .consent__check:focus-visible + .consent__box {
      outline: 2px solid var(--color-primary-500);
      outline-offset: 2px;
    }
    .consent__text {
      line-height: 1.55;
      color: var(--text-basic-color);
    }
    .consent__text a { font-weight: 700; }
    .consent__req {
      margin-inline-start: 0.3em;
      font-size: 0.85em;
      color: var(--text-hint-color);
    }
    .consent__sub {
      display: block;
      margin-top: 0.2rem;
      font-size: 0.85rem;
      color: var(--text-hint-color);
    }
    .consent__error {
      margin: 0.25rem 0 0.5rem;
      font-size: 0.9rem;
      color: var(--color-danger-600);
    }
    .consent__go { margin-top: 0.75rem; }
    .consent__later { margin-top: 0.35rem; }
    .spin { animation: consent-spin 1s linear infinite; }
    @keyframes consent-spin { to { transform: rotate(360deg); } }
  `],
})
export class OwnerConsentDialogComponent {
  /** First name for the greeting; empty greets without one. */
  @Input() name = '';

  terms = false;
  marketing = false;
  missing = false;
  saving = false;
  error = '';

  constructor(
    public ref: NbDialogRef<OwnerConsentDialogComponent>,
    private consent: ConsentService,
  ) {}

  async confirm(): Promise<void> {
    if (this.saving) return;
    this.error = '';
    if (!this.terms) {
      this.missing = true;
      return;
    }
    this.saving = true;
    try {
      await this.consent.accept(this.marketing);
      this.ref.close(true);
    } catch (err: any) {
      this.error = err?.status === 0
        ? 'אין חיבור לשרת. בדקו את החיבור לאינטרנט ונסו שוב.'
        : 'לא הצלחנו לשמור את האישור. נסו שוב בעוד רגע.';
    } finally {
      this.saving = false;
    }
  }
}
