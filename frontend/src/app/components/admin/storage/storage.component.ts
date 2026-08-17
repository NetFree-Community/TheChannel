import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbToggleModule,
  NbProgressBarModule, NbToastrService, NbAlertModule, NbIconModule
} from '@nebular/theme';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

interface StorageInfo {
  usedBytes: number;
  quotaBytes: number;
  usedPercent: number;
  autoCleanup: boolean;
  level: 'ok' | 'warning' | 'critical';
}

@Component({
  selector: 'app-channel-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbToggleModule,
    NbProgressBarModule, NbAlertModule, NbIconModule
  ],
  template: `
    <nb-card>
      <nb-card-header>אחסון</nb-card-header>
      <nb-card-body>
        @if (info) {
          <div class="mb-3">
            <div class="d-flex justify-content-between mb-1">
              <span>שימוש: {{ formatBytes(info.usedBytes) }}</span>
              <span>מתוך: {{ formatBytes(info.quotaBytes) }}</span>
            </div>
            <nb-progress-bar
              [value]="info.usedPercent"
              [status]="progressStatus()"
              [displayValue]="true">
            </nb-progress-bar>
          </div>

          @if (info.level === 'critical') {
            <nb-alert status="danger" closable class="mb-3">
              <nb-icon icon="alert-triangle-outline"></nb-icon>
              שטח האחסון כמעט מלא! פנה מקום או הפעל ניקוי אוטומטי.
            </nb-alert>
          } @else if (info.level === 'warning') {
            <nb-alert status="warning" closable class="mb-3">
              <nb-icon icon="alert-circle-outline"></nb-icon>
              שטח האחסון מתמלא ({{ info.usedPercent | number:'1.0-0' }}%).
            </nb-alert>
          }

          <div class="d-flex align-items-center gap-3">
            <nb-toggle
              [(ngModel)]="info.autoCleanup"
              (change)="saveAutoCleanup()">
              ניקוי אוטומטי של מדיה ישנה
            </nb-toggle>
            <small class="text-muted">
              כשהאחסון עומד לעגמור, מוחק קבצים ישנים אוטומטית כדי לפנות מקום
            </small>
          </div>
        } @else {
          <p>טוען...</p>
        }
      </nb-card-body>
    </nb-card>
  `
})
export class StorageComponent implements OnInit {
  @Input() slug!: string;

  info?: StorageInfo;

  constructor(private http: HttpClient, private toastr: NbToastrService) {}

  ngOnInit() {
    this.load();
  }

  async load() {
    try {
      this.info = await firstValueFrom(
        this.http.get<StorageInfo>(`/api/channel/${this.slug}/admin/storage`)
      );
    } catch {
      this.toastr.danger('שגיאה בטעינת מידע אחסון', 'שגיאה');
    }
  }

  async saveAutoCleanup() {
    if (!this.info) return;
    try {
      await firstValueFrom(this.http.post(
        `/api/channel/${this.slug}/admin/storage/auto-cleanup`,
        { enabled: this.info.autoCleanup }
      ));
      this.toastr.success('הגדרות נשמרו', 'אחסון');
    } catch {
      this.toastr.danger('שגיאה בשמירה', 'שגיאה');
    }
  }

  progressStatus(): string {
    if (!this.info) return 'primary';
    if (this.info.level === 'critical') return 'danger';
    if (this.info.level === 'warning') return 'warning';
    return 'success';
  }

  formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const gb = bytes / (1024 ** 3);
    if (gb >= 1) return gb.toFixed(2) + ' GB';
    const mb = bytes / (1024 ** 2);
    if (mb >= 1) return mb.toFixed(1) + ' MB';
    return (bytes / 1024).toFixed(0) + ' KB';
  }
}
