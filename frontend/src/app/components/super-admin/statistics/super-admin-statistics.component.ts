import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';
import { ConfirmService } from '../../../services/confirm.service';

@Component({
  selector: 'app-super-admin-statistics',
  standalone: true,
  imports: [
    CommonModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
  ],
  templateUrl: './super-admin-statistics.component.html',
  styleUrl: './super-admin-statistics.component.scss',
})
export class SuperAdminStatisticsComponent implements OnInit {
  magnetStats: any = null;
  loadingStats = true;
  resetting = false;
  /** Shown in the magnet card instead of the generic "no data" line. */
  magnetError = '';
  /** True when the key is simply not set up — a setup hint, not a failure. */
  magnetNotConfigured = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
  ) {}

  ngOnInit(): void {
    this.loadMagnetStats();
  }

  loadMagnetStats() {
    this.loadingStats = true;
    this.magnetError = '';
    this.magnetNotConfigured = false;
    this.superAdminService.getMagnetStats()
      .then(stats => this.magnetStats = stats)
      .catch(err => {
        // A missing key is the normal state of a platform that never used
        // Magnet, not an outage: the backend answers 400 with a precise
        // {"error":"missing_api_key"} body, which used to be collapsed into the
        // same warning toast as an upstream failure on every visit.
        const body = err?.error;
        const code = typeof body === 'string' ? body : (body?.error ?? '');
        if (err?.status === 400 && String(code).includes('missing_api_key')) {
          this.magnetNotConfigured = true;
          this.magnetError = 'מפתח ה-API של מגנט עדיין לא הוגדר';
          return;
        }
        this.magnetError = err?.status === 502
          ? 'מגנט לא ענה. נסו שוב בעוד רגע.'
          : 'הנתונים ממגנט לא נטענו. נסו שוב בעוד רגע.';
        this.toastr.warning('', 'הנתונים ממגנט לא נטענו');
      })
      .finally(() => this.loadingStats = false);
  }

  async resetStatistics() {
    // The reset clears the recorded peak AND every channel's monthly
    // connection-history series (the graphs on the owners' statistics
    // screens). The prompt used to name only the peak.
    const ok = await this.confirm.ask({
      title: 'לאפס את מוני החיבורים של כל הערוצים?',
      message: 'שיא החיבורים והיסטוריית החיבורים של כל הערוצים (הגרפים במסכי הסטטיסטיקה של מנהלי הערוצים) יימחקו. אי אפשר לשחזר אותם.',
      status: 'danger',
      confirmLabel: 'איפוס',
    });
    if (!ok) return;
    this.resetting = true;
    this.superAdminService.resetStatistics()
      .then(() => this.toastr.success('', 'מוני החיבורים של כל הערוצים אופסו'))
      .catch((err) => this.toastr.danger('', err?.status === 504
        ? 'האיפוס לא הסתיים בזמן — חלק מהערוצים אופסו. הריצו שוב כדי להשלים.'
        : 'האיפוס לא הצליח, נסו שוב'))
      .finally(() => this.resetting = false);
  }

  /**
   * The upstream response is nested (site, clicks, earnings objects). Only
   * the top level used to be listed, so those rows read "[object Object]";
   * nested values are flattened to dotted keys.
   */
  getStatEntries(): { key: string; value: any }[] {
    if (!this.magnetStats || typeof this.magnetStats !== 'object') return [];
    const rows: { key: string; value: any }[] = [];
    const walk = (value: any, prefix: string) => {
      if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
        for (const [k, v] of Object.entries(value)) {
          walk(v, prefix ? `${prefix}.${k}` : k);
        }
        return;
      }
      rows.push({ key: prefix, value: Array.isArray(value) ? JSON.stringify(value) : value });
    };
    walk(this.magnetStats, '');
    return rows;
  }
}
