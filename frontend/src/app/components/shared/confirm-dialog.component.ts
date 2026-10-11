import { Component, Input } from '@angular/core';
import { NbButtonModule, NbCardModule, NbDialogRef, NbIconModule } from '@nebular/theme';

export type ConfirmStatus = 'primary' | 'danger' | 'warning';

export interface ConfirmOptions {
  title: string;
  /** One or two sentences saying what happens, in the user's words. */
  message?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  /** 'danger' for anything that cannot be undone. */
  status?: ConfirmStatus;
  /** Eva icon name. */
  icon?: string;
}

/**
 * The in-app replacement for window.confirm(): styled like the rest of the
 * app, readable on a phone, closable with Escape or the backdrop, and the
 * cancel button is the safe default. Opened through ConfirmService.
 */
@Component({
  selector: 'app-confirm-dialog',
  standalone: true,
  imports: [NbCardModule, NbButtonModule, NbIconModule],
  template: `
    <nb-card class="confirm" role="alertdialog" aria-modal="true"
             aria-labelledby="confirm-title" aria-describedby="confirm-message">
      <nb-card-body class="confirm__body">
        <div class="confirm__icon confirm__icon--{{ status }}" aria-hidden="true">
          <nb-icon [icon]="icon"></nb-icon>
        </div>
        <h2 class="confirm__title" id="confirm-title">{{ title }}</h2>
        @if (message) {
          <p class="confirm__message" id="confirm-message">{{ message }}</p>
        }
        <div class="confirm__actions">
          <button nbButton ghost status="basic" type="button" (click)="close(false)">
            {{ cancelLabel }}
          </button>
          <button nbButton [status]="status" type="button" (click)="close(true)">
            {{ confirmLabel }}
          </button>
        </div>
      </nb-card-body>
    </nb-card>
  `,
  styles: [`
    .confirm {
      width: min(92vw, 420px);
      margin: 0;
    }
    .confirm__body {
      text-align: center;
      padding: 2rem 1.5rem 1.5rem;
    }
    .confirm__icon {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 3.25rem;
      height: 3.25rem;
      border-radius: 50%;
      margin-bottom: 1rem;
      font-size: 1.6rem;
      background: var(--color-primary-100);
      color: var(--color-primary-600);
    }
    .confirm__icon--danger {
      background: var(--color-danger-100);
      color: var(--color-danger-600);
    }
    .confirm__icon--warning {
      background: var(--color-warning-100);
      color: var(--color-warning-700);
    }
    .confirm__icon nb-icon {
      font-size: inherit;
      width: 1em;
      height: 1em;
    }
    .confirm__title {
      margin: 0 0 0.5rem;
      font-size: 1.2rem;
      font-weight: 700;
      color: var(--text-basic-color);
    }
    .confirm__message {
      margin: 0 0 1.5rem;
      line-height: 1.6;
      color: var(--text-hint-color);
    }
    .confirm__actions {
      display: flex;
      justify-content: center;
      gap: 0.5rem;
      flex-wrap: wrap;
    }
    .confirm__actions button {
      min-width: 7rem;
    }
  `],
})
export class ConfirmDialogComponent {
  @Input() title = 'לאשר את הפעולה?';
  @Input() message = '';
  @Input() confirmLabel = 'אישור';
  @Input() cancelLabel = 'ביטול';
  @Input() status: ConfirmStatus = 'primary';
  @Input() icon = 'question-mark-circle-outline';

  constructor(private ref: NbDialogRef<ConfirmDialogComponent>) {}

  close(confirmed: boolean): void {
    this.ref.close(confirmed);
  }
}
