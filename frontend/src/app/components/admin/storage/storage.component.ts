import { Component, EventEmitter, Input, OnInit, Output } from '@angular/core';
import { formatBytes, storageLevelStatus } from '../../../utils/storage-format';
import {
  NbCardModule, NbButtonModule, NbToggleModule,
  NbProgressBarModule, NbToastrService, NbAlertModule, NbIconModule, NbSpinnerModule,
} from '@nebular/theme';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';
import { ConfirmService } from '../../../services/confirm.service';

interface StorageInfo {
  usedBytes: number;
  quotaBytes: number;
  usedPercent: number;
  autoCleanup: boolean;
  level: 'ok' | 'warning' | 'critical';
}

/**
 * "אחסון": how much of the channel's file quota is used, and the one switch an
 * owner has when it runs out — auto-cleanup, which the server applies at the
 * moment an upload would not fit (oldest files first, down to 80 % of the
 * quota, the logo excepted). Everything else about the quota belongs to the
 * platform operator.
 */
@Component({
  selector: 'app-channel-storage',
  standalone: true,
  imports: [
    NbCardModule, NbButtonModule, NbToggleModule,
    NbProgressBarModule, NbAlertModule, NbIconModule, NbSpinnerModule,
  ],
  templateUrl: './storage.component.html',
  styleUrl: './storage.component.scss',
})
export class StorageComponent implements OnInit {
  @Input() slug!: string;
  /** The storage endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  info?: StorageInfo;
  /**
   * Mirrors info.autoCleanup for the toggle. The switch flips itself on tap,
   * before anything is confirmed or saved; writing the stored value back here
   * is what moves it back when the owner cancels or the save fails.
   */
  cleanupChecked = false;
  loading = true;
  loadFailed = false;
  saving = false;

  constructor(
    private http: HttpClient,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
  ) {}

  ngOnInit() {
    this.load();
  }

  get unlimited(): boolean {
    return !!this.info && !this.info.quotaBytes;
  }

  get freeBytes(): number {
    if (!this.info || !this.info.quotaBytes) return 0;
    return Math.max(0, this.info.quotaBytes - this.info.usedBytes);
  }

  async load() {
    this.loading = true;
    this.loadFailed = false;
    try {
      this.info = await firstValueFrom(
        this.http.get<StorageInfo>(`/api/channel/${this.slug}/admin/storage`)
      );
      this.cleanupChecked = !!this.info?.autoCleanup;
    } catch (err: any) {
      this.loadFailed = true;
      if (err?.status === 403 || err?.status === 401) {
        this.accessDenied.emit();
      } else {
        this.toastr.danger('', 'לא הצלחנו לטעון את נתוני האחסון — נסו שוב');
      }
    } finally {
      this.loading = false;
    }
  }

  /**
   * The toggle is bound one-way so the switch only moves once the owner has
   * confirmed (when turning cleanup on — it deletes files) and the server has
   * stored the new state.
   */
  async onAutoCleanupChange(enabled: boolean) {
    if (!this.info || this.saving) return;
    this.cleanupChecked = enabled;
    if (enabled === this.info.autoCleanup) return;
    if (enabled) {
      const ok = await this.confirm.ask({
        title: 'להפעיל ניקוי אוטומטי?',
        message: 'כשהמקום ייגמר, הקבצים הישנים ביותר יימחקו מעצמם כדי לפנות מקום להעלאות חדשות. קובץ שנמחק אי אפשר להחזיר, והקישור אליו בהודעה הישנה יפסיק לעבוד.',
        confirmLabel: 'הפעלה',
        status: 'warning',
        icon: 'trash-2-outline',
      });
      if (!ok) {
        this.cleanupChecked = this.info.autoCleanup;
        return;
      }
    }
    await this.saveAutoCleanup(enabled);
  }

  private async saveAutoCleanup(enabled: boolean) {
    if (!this.info) return;
    this.saving = true;
    try {
      await firstValueFrom(this.http.post(
        `/api/channel/${this.slug}/admin/storage/auto-cleanup`,
        { enabled }
      ));
      this.info = { ...this.info, autoCleanup: enabled };
      this.cleanupChecked = enabled;
      this.toastr.success('', enabled ? 'הניקוי האוטומטי הופעל' : 'הניקוי האוטומטי כובה');
    } catch (err: any) {
      // Back to what the server still stores.
      this.cleanupChecked = this.info.autoCleanup;
      this.toastr.danger('', err?.status === 403 || err?.status === 401
        ? 'רק בעלי הערוץ יכולים לשנות את הניקוי האוטומטי'
        : 'השינוי לא נשמר — נסו שוב');
    } finally {
      this.saving = false;
    }
  }

  // The server sends the raw float division and nb-progress-bar prints
  // `{{ value }}%` verbatim: 2.3456789012345% on a quiet channel, and once a
  // quota is lowered below current usage, 130% spilling past the bar. A quota
  // of 0 means unlimited, so there is nothing to be a percentage of.
  percentDisplay(): number {
    if (!this.info || !this.info.quotaBytes) return 0;
    return Math.min(100, Math.round(this.info.usedPercent));
  }

  progressStatus(): string {
    // Before info loads there is no level yet; keep the neutral colour.
    if (!this.info) return 'primary';
    return storageLevelStatus(this.info.level);
  }

  formatBytes = formatBytes;
}
