import { Component, EventEmitter, Output } from '@angular/core';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';
import { ConfirmService } from '../../../services/confirm.service';
import { MagnetStatsPanelComponent } from '../../shared/magnet-stats-panel.component';

@Component({
  selector: 'app-super-admin-statistics',
  standalone: true,
  imports: [
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    MagnetStatsPanelComponent,
  ],
  templateUrl: './super-admin-statistics.component.html',
  styleUrl: './super-admin-statistics.component.scss',
})
export class SuperAdminStatisticsComponent {
  /** The key is fixed on the "פרסומות מגנט" screen; the panel switches to it. */
  @Output() openMagnet = new EventEmitter<void>();

  resetting = false;

  /** The platform's own site, read with the global key. A stable reference:
   *  the panel reloads whenever its source changes. */
  readonly loadMagnetStats = () => this.superAdminService.getMagnetStats();

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
  ) {}

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
}
