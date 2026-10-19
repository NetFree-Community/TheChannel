import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbIconModule, NbToastrService
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbInputModule,
    NbFormFieldModule, NbIconModule
  ],
  templateUrl: './global-storage.component.html',
  styleUrl: './global-storage.component.scss',
})
export class GlobalStorageComponent implements OnInit {
  // null when the number input is cleared (ngModel posts null, which the
  // server reads as 0).
  defaultQuotaGb: number | null = 5;
  loading = true;
  loadFailed = false;
  saving = false;
  /** The value on the server, to tell the operator about an unsaved edit. */
  private savedQuotaGb: number | null = null;

  /**
   * A stored 0 is not "use the default": the upload path treats an effective
   * quota of 0 as unlimited, so an empty or 0 field silently lifted every
   * channel's limit. min="1" on the input is only a hint.
   */
  get quotaInvalid(): boolean {
    const gb = Number(this.defaultQuotaGb);
    return this.defaultQuotaGb === null || this.defaultQuotaGb === undefined
      || !Number.isFinite(gb) || gb < 1;
  }

  get dirty(): boolean {
    return this.savedQuotaGb !== null && Number(this.defaultQuotaGb) !== this.savedQuotaGb;
  }

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService
  ) {}

  ngOnInit() {
    this.load();
  }

  async load() {
    this.loading = true;
    this.loadFailed = false;
    try {
      const cfg = await this.superAdminService.getGlobalStorageConfig();
      this.defaultQuotaGb = cfg.defaultQuotaGb;
      this.savedQuotaGb = Number(cfg.defaultQuotaGb);
    } catch {
      this.loadFailed = true;
      this.toastr.danger('', 'הגדרות האחסון לא נטענו');
    } finally {
      this.loading = false;
    }
  }

  async save() {
    if (this.quotaInvalid) return;
    this.saving = true;
    try {
      await this.superAdminService.setGlobalStorageConfig(Number(this.defaultQuotaGb));
      this.savedQuotaGb = Number(this.defaultQuotaGb);
      this.toastr.success('', `ברירת המחדל עודכנה ל-${this.savedQuotaGb} GB לכל ערוץ`);
    } catch (err: any) {
      this.toastr.danger('', err?.status === 400
        ? 'יש להזין נפח חיובי של ג׳יגה-בייט'
        : 'השמירה לא הצליחה, נסו שוב');
    } finally {
      this.saving = false;
    }
  }
}
