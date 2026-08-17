import { Component, Input, OnInit, OnChanges } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbProgressBarModule, NbToastrService, NbAlertModule, NbIconModule
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
    NbFormFieldModule, NbProgressBarModule, NbAlertModule, NbIconModule
  ],
  template: `
    <nb-card>
      <nb-card-header>אחסון — {{ slug }}</nb-card-header>
      <nb-card-body>
        @if (config) {
          <!-- Usage bar -->
          <div class="mb-3">
            <div class="d-flex justify-content-between mb-1">
              <span>{{ formatBytes(config.storageInfo.usedBytes) }} בשימוש</span>
              <span>מתוך {{ formatBytes(config.storageInfo.quotaBytes) }}</span>
            </div>
            <nb-progress-bar
              [value]="config.storageInfo.usedPercent"
              [status]="progressStatus(config.storageInfo.level)"
              [displayValue]="true">
            </nb-progress-bar>
            @if (config.storageInfo.autoCleanup) {
              <small class="text-muted">ניקוי אוטומטי פעיל</small>
            }
          </div>

          <!-- Quota override -->
          <div class="d-flex align-items-center gap-3">
            <nb-form-field>
              <nb-icon nbPrefix icon="hard-drive-outline"></nb-icon>
              <input nbInput type="number" min="0" step="0.5"
                    [(ngModel)]="config.quotaGb"
                    placeholder="GB (0 = ברירת מחדל גלובלית)">
            </nb-form-field>
            <span class="text-muted">GB (0 = ברירת מחדל גלובלית)</span>
            <button nbButton status="primary" size="small" (click)="save()">שמור</button>
          </div>
        } @else {
          <p>טוען...</p>
        }
      </nb-card-body>
    </nb-card>
  `
})
export class SuperAdminStorageComponent implements OnInit, OnChanges {
  @Input() slug!: string;

  config?: ChannelStorageConfig;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService
  ) {}

  ngOnInit() { this.load(); }
  ngOnChanges() { this.load(); }

  async load() {
    if (!this.slug) return;
    try {
      this.config = await this.superAdminService.getChannelStorage(this.slug);
    } catch {
      this.toastr.danger('שגיאה בטעינת מידע אחסון', 'שגיאה');
    }
  }

  async save() {
    if (!this.config) return;
    try {
      await this.superAdminService.setChannelStorage(this.slug, this.config.quotaGb);
      this.toastr.success('קוטה עודכנה', 'אחסון');
      this.load();
    } catch {
      this.toastr.danger('שגיאה בשמירה', 'שגיאה');
    }
  }

  progressStatus(level: string): string {
    if (level === 'critical') return 'danger';
    if (level === 'warning') return 'warning';
    return 'success';
  }

  formatBytes(bytes: number): string {
    if (!bytes) return '0 B';
    const gb = bytes / (1024 ** 3);
    if (gb >= 1) return gb.toFixed(2) + ' GB';
    const mb = bytes / (1024 ** 2);
    if (mb >= 1) return mb.toFixed(1) + ' MB';
    return (bytes / 1024).toFixed(0) + ' KB';
  }
}
