import { Component, Input, OnChanges } from '@angular/core';
import { formatBytes, storageLevelStatus } from '../../../utils/storage-format';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbProgressBarModule, NbToastrService, NbIconModule
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

interface ChannelStorageConfig {
  quotaGb: number;
  storageInfo: {
    usedBytes: number;
    quotaBytes: number;
    usedPercent: number;
    autoCleanup: boolean;
    level: string;
  };
}

@Component({
  selector: 'app-super-admin-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbInputModule,
    NbFormFieldModule, NbProgressBarModule, NbIconModule
  ],
  templateUrl: './super-admin-storage.component.html',
  styleUrl: './super-admin-storage.component.scss',
})
export class SuperAdminStorageComponent implements OnChanges {
  @Input() slug!: string;

  config?: ChannelStorageConfig;
  loading = true;
  loadFailed = false;
  saving = false;
  // Separate from config.quotaGb so a cleared field (ngModel posts null)
  // can be refused before anything is sent.
  quotaGb: number | null = 0;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService
  ) {}

  // ngOnChanges fires for the initial slug binding too, so no ngOnInit needed.
  ngOnChanges() { this.load(); }

  /** 0 means "use the system default"; anything negative or empty is refused. */
  get quotaInvalid(): boolean {
    const gb = Number(this.quotaGb);
    return this.quotaGb === null || this.quotaGb === undefined || !Number.isFinite(gb) || gb < 0;
  }

  get usesDefault(): boolean {
    return Number(this.quotaGb) === 0;
  }

  /** Percent capped for the bar; the label still shows the true number. */
  get barValue(): number {
    const pct = Number(this.config?.storageInfo?.usedPercent) || 0;
    return Math.max(0, Math.min(100, Math.round(pct)));
  }

  async load() {
    if (!this.slug) {
      // No channel to show: better an honest error card than a spinner forever.
      this.loading = false;
      this.loadFailed = true;
      return;
    }
    this.loading = true;
    this.loadFailed = false;
    try {
      this.config = await this.superAdminService.getChannelStorage(this.slug);
      this.quotaGb = Number(this.config?.quotaGb) || 0;
    } catch (err: any) {
      this.loadFailed = true;
      this.toastr.danger('', err?.status === 404
        ? 'הערוץ לא נמצא — ייתכן שנמחק'
        : 'נתוני האחסון של הערוץ לא נטענו');
    } finally {
      this.loading = false;
    }
  }

  async save() {
    if (!this.config || this.quotaInvalid) return;
    this.saving = true;
    try {
      await this.superAdminService.setChannelStorage(this.slug, Number(this.quotaGb));
      this.toastr.success('', this.usesDefault
        ? 'הערוץ חזר לנפח ברירת המחדל של המערכת'
        : `הנפח של הערוץ עודכן ל-${Number(this.quotaGb)} GB`);
      this.load();
    } catch (err: any) {
      if (err?.status === 400) {
        this.toastr.danger('', 'הנפח חייב להיות מספר 0 ומעלה');
      } else if (err?.status === 404) {
        this.toastr.danger('', 'הערוץ לא נמצא — ייתכן שנמחק');
      } else {
        this.toastr.danger('', 'השמירה לא הצליחה, נסו שוב');
      }
    } finally {
      this.saving = false;
    }
  }

  progressStatus = storageLevelStatus;
  formatBytes = formatBytes;
}
