import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

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
})
export class SuperAdminStatisticsComponent implements OnInit {
  magnetStats: any = null;
  loadingStats = true;
  resetting = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadMagnetStats();
  }

  loadMagnetStats() {
    this.loadingStats = true;
    this.superAdminService.getMagnetStats()
      .then(stats => this.magnetStats = stats)
      .catch(() => this.toastr.warning('', 'לא ניתן לטעון סטטיסטיקות מגנט'))
      .finally(() => this.loadingStats = false);
  }

  resetStatistics() {
    if (!confirm('האם אתה בטוח שברצונך לאפס את שיא החיבורים?')) return;
    this.resetting = true;
    this.superAdminService.resetStatistics()
      .then(() => this.toastr.success('', 'שיא החיבורים אופס בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה באיפוס הסטטיסטיקות'))
      .finally(() => this.resetting = false);
  }

  getStatEntries(): { key: string; value: any }[] {
    if (!this.magnetStats || typeof this.magnetStats !== 'object') return [];
    return Object.entries(this.magnetStats).map(([key, value]) => ({ key, value }));
  }
}
