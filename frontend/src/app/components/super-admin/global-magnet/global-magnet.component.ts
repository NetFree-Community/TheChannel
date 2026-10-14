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

/** The shape Magnet mints: "mgs_" and 48 lowercase hex characters. */
const STATS_KEY_RE = /^mgs_[0-9a-f]{48}$/;

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
    statsKeySet: false,
  };

  /**
   * A new stats key typed by the operator. Kept outside `config` and never
   * pre-filled: the server does not send the stored key back, and an empty
   * field on save means "keep the stored one".
   */
  statsKeyInput = '';
  /** Set by "מחיקת המפתח"; the key is removed on the next save. */
  clearStatsKey = false;
  statsKeyError = '';

  loading = true;
  loadFailed = false;
  saving = false;
  newLockedChannel = '';
  /** The embed key is public, but masked by default like any key field. */
  showApiKey = false;
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  // These are the values every consumer understands (MagnetAdsService and the
  // per-channel form): anything else silently falls back to message spacing.
  modeOptions = [
    { value: 'by_messages', label: 'לפי מספר הודעות', hint: 'פרסומת אחרי כל כמה הודעות שהקורא גלל.' },
    { value: 'by_time', label: 'לפי זמן', hint: 'פרסומת אחרי כל פרק זמן בין שעות הפרסום של ההודעות (לא זמן הקריאה).' },
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
      .then(cfg => this.applyServerConfig(cfg))
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

  private applyServerConfig(cfg: GlobalMagnetConfig) {
    this.config = {
      ...this.config,
      ...cfg,
      mode: this.normalizeMode(cfg?.mode),
      lockedChannels: [...(cfg?.lockedChannels || [])],
      statsKeySet: !!cfg?.statsKeySet,
    };
    this.statsKeyInput = '';
    this.clearStatsKey = false;
    this.statsKeyError = '';
    this.snapshot = JSON.stringify(this.config);
  }

  get dirty(): boolean {
    return !!this.snapshot && (
      JSON.stringify(this.config) !== this.snapshot ||
      !!this.statsKeyInput.trim() ||
      this.clearStatsKey
    );
  }

  get statsKeyPlaceholder(): string {
    return this.config.statsKeySet ? 'מוגדר — השאירו ריק כדי לשמור את המפתח הקיים' : 'mgs_…';
  }

  markStatsKeyForClearing() {
    this.clearStatsKey = true;
    this.statsKeyInput = '';
    this.statsKeyError = '';
  }

  cancelStatsKeyClearing() {
    this.clearStatsKey = false;
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
    const statsKey = this.statsKeyInput.trim();
    // Same rule as the server: catching a mis-paste here keeps the rest of
    // the form from being saved alongside a key that would only fail later.
    if (statsKey && !this.clearStatsKey && !STATS_KEY_RE.test(statsKey)) {
      this.statsKeyError = 'מפתח הסטטיסטיקה אינו תקין — הוא מתחיל ב-mgs_ ואחריו 48 תווים. העתיקו אותו שוב ממגנט.';
      this.toastr.danger(this.statsKeyError, 'השמירה לא בוצעה');
      return;
    }
    this.statsKeyError = '';
    this.saving = true;
    this.superAdminService.setMagnetConfig({
      ...this.config,
      statsKey: this.clearStatsKey ? '' : statsKey,
      clearStatsKey: this.clearStatsKey,
    })
      .then(cfg => {
        this.applyServerConfig(cfg);
        this.toastr.success('', 'הגדרות המגנט נשמרו');
      })
      .catch(err => {
        // The server's own Hebrew message when it rejected the key.
        const msg = err?.status === 400 && err?.error?.error === 'invalid_stats_key' ? err.error.message : '';
        if (msg) this.statsKeyError = msg;
        this.toastr.danger(msg, 'השמירה לא הצליחה, נסו שוב');
      })
      .finally(() => this.saving = false);
  }
}
