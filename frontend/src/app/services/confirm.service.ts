import { Injectable } from '@angular/core';
import { NbDialogService } from '@nebular/theme';
import { firstValueFrom } from 'rxjs';
import { ConfirmDialogComponent, ConfirmOptions } from '../components/shared/confirm-dialog.component';

/**
 * `await confirm.ask({...})` → true only when the user pressed the confirm
 * button. Backdrop, Escape and the cancel button all resolve false, so a
 * caller can never mistake "closed" for "confirmed".
 */
@Injectable({ providedIn: 'root' })
export class ConfirmService {
  constructor(private dialog: NbDialogService) {}

  async ask(options: ConfirmOptions): Promise<boolean> {
    const defaults: Partial<ConfirmOptions> = {};
    if (options.status === 'danger') {
      defaults.icon = 'alert-triangle-outline';
      defaults.confirmLabel = 'מחיקה';
    }
    const ref = this.dialog.open(ConfirmDialogComponent, {
      context: { ...defaults, ...options },
      closeOnBackdropClick: true,
      closeOnEsc: true,
      autoFocus: true,
    });
    const result = await firstValueFrom(ref.onClose);
    return result === true;
  }
}
