import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
  NbSelectModule,
  NbTagModule,
  NbToggleModule,
  NbToastrService,
  NbTooltipModule,
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
    NbAlertModule,
    NbTagModule,
    NbFormFieldModule,
    NbTooltipModule,
  ],
  templateUrl: './global-magnet.component.html',
  styleUrl: './global-magnet.component.scss',
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

  loading = true;
  loadFailed = false;
  saving = false;
  newLockedChannel = '';
  /** The API key is a secret: masked until the operator asks to see it. */
  showApiKey = false;
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  // These are the values every consumer understands (MagnetAdsService and the
  // per-channel form): anything else silently falls back to message spacing.
  modeOptions = [
    { value: 'by_messages', label: 'לפי מספר הודעות', hint: 'פרסומת אחרי כל כמה הודעות שהקורא גלל.' },
    { value: 'by_time', label: 'לפי זמן', hint: 'פרסומת אחרי כל כמה שניות של קריאה.' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  load() {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getMagnetConfig()
      .then(cfg => {
        this.config = {
          ...this.config,
          ...cfg,
          mode: this.normalizeMode(cfg?.mode),
          lockedChannels: [...(cfg?.lockedChannels || [])],
        };
        this.snapshot = JSON.stringify(this.config);
      })
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'הגדרות המגנט לא נטענו');
      })
      .finally(() => this.loading = false);
  }

  // Values saved by an earlier version of this form (per_messages/per_seconds)
  // are migrated on load, so the next save stores the value clients read.
  private normalizeMode(mode: string | undefined): string {
    return mode === 'by_time' || mode === 'per_seconds' ? 'by_time' : 'by_messages';
  }

  get dirty(): boolean {
    return !!this.snapshot && JSON.stringify(this.config) !== this.snapshot;
  }

  get byTime(): boolean {
    return this.config.mode === 'by_time';
  }

  modeHint(mode: string): string {
    return this.modeOptions.find(o => o.value === mode)?.hint || '';
  }

  /** Whether the saved settings reach any channel at all. */
  get appliesNowhere(): boolean {
    return this.config.enabled && !this.config.lockAll && !this.config.lockedChannels.length;
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
      .then(() => {
        this.snapshot = JSON.stringify(this.config);
        this.toastr.success('', 'הגדרות המגנט נשמרו');
      })
      .catch(() => this.toastr.danger('', 'השמירה לא הצליחה, נסו שוב'))
      .finally(() => this.saving = false);
  }
}
