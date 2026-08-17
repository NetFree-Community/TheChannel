import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbSelectModule,
  NbToggleModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, GlobalMagnetConfig } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-magnet',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbSelectModule,
    NbToggleModule,
  ],
  templateUrl: './global-magnet.component.html',
})
export class GlobalMagnetComponent implements OnInit {
  config: GlobalMagnetConfig = {
    enabled: false,
    snippet: '',
    mode: 'by_messages',
    perMessages: 10,
    minTimeSeconds: 60,
    perSeconds: 300,
    minMessagesSinceLast: 5,
    apiKey: '',
    lockAll: false,
    lockedChannels: [],
  };

  saving = false;
  newLockedChannel = '';

  // These are the values every consumer understands (MagnetAdsService and the
  // per-channel form): anything else silently falls back to message spacing.
  modeOptions = [
    { value: 'by_messages', label: 'לפי מספר הודעות' },
    { value: 'by_time', label: 'לפי זמן (שניות)' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.superAdminService.getMagnetConfig()
      .then(cfg => this.config = {
        ...cfg,
        mode: this.normalizeMode(cfg.mode),
        lockedChannels: [...(cfg.lockedChannels || [])],
      })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת הגדרות מגנט'));
  }

  // Values saved by an earlier version of this form (per_messages/per_seconds)
  // are migrated on load, so the next save stores the value clients read.
  private normalizeMode(mode: string | undefined): string {
    return mode === 'by_time' || mode === 'per_seconds' ? 'by_time' : 'by_messages';
  }

  addLockedChannel() {
    const slug = this.newLockedChannel.trim();
    if (!slug) return;
    if (!this.config.lockedChannels.includes(slug)) {
      this.config.lockedChannels.push(slug);
    }
    this.newLockedChannel = '';
  }

  removeLockedChannel(slug: string) {
    this.config.lockedChannels = this.config.lockedChannels.filter(s => s !== slug);
  }

  save() {
    this.saving = true;
    this.superAdminService.setMagnetConfig(this.config)
      .then(() => this.toastr.success('', 'הגדרות מגנט נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת הגדרות מגנט'))
      .finally(() => this.saving = false);
  }
}
