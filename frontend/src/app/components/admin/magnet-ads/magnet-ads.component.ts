import { Component, ElementRef, EventEmitter, OnInit, Output, ViewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbRadioModule,
  NbSpinnerModule,
  NbToastrService,
  NbToggleModule,
  NbTooltipModule,
} from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { AuthService } from '../../../services/auth.service';
import { ConfirmService } from '../../../services/confirm.service';
import { MagnetAdsService } from '../../../services/magnet-ads.service';
import { Setting } from '../../../models/setting.model';
import { toBool } from '../settings/settings.schema';
import { MagnetStatsPanelComponent } from '../../shared/magnet-stats-panel.component';

type MagnetMode = 'by_messages' | 'by_time';

/**
 * The channel's own Magnet numbers, as the server decides them:
 * - locked: the channel serves the platform's embed and the earnings are the
 *   platform's, so its managers get nothing (the super admin gets a pointer
 *   to the platform's statistics screen);
 * - ready: the channel runs its own embed, so it may read its own numbers
 *   with a stats key from its own Magnet account.
 */
type OwnStatsState = 'loading' | 'locked' | 'ready' | 'error';

/** The shape rotate_site_stats_key mints; backend/ads.go checks the same. */
const STATS_KEY_RE = /^mgs_[0-9a-f]{48}$/;

const MAGNET_KEYS = [
  'magnet_enabled',
  'magnet_snippet',
  'magnet_mode',
  'magnet_per_messages',
  'magnet_min_time_seconds',
  'magnet_per_seconds',
  'magnet_min_messages_since',
  // Saved by an old UI but consumed by nothing — recognised so it is pruned
  // from the payload on the next save instead of surviving as an "other" setting.
  'magnet_api_key',
] as const;

@Component({
  selector: 'app-magnet-ads',
  imports: [
    FormsModule,
    RouterLink,
    NbAlertModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbRadioModule,
    NbSpinnerModule,
    NbTooltipModule,
    MagnetStatsPanelComponent,
  ],
  templateUrl: './magnet-ads.component.html',
  styleUrl: './magnet-ads.component.scss',
})
export class MagnetAdsComponent implements OnInit {
  /** The settings endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  enabled = false;
  snippet = '';
  mode: MagnetMode = 'by_messages';
  perMessages = 5;
  minTimeSeconds = 0;
  perSeconds = 60;
  minMessagesSince = 0;

  otherSettings: Setting[] = [];
  inProgress = false;
  // Save stays disabled until the server copy arrived: settings/set replaces
  // the whole blob (no merge), so a save after a failed load dropped every
  // non-magnet key (api_secret_key, webhook_*, regex rules) along with it.
  loaded = false;
  loading = true;
  loadFailed = false;
  // Super-admin lock on this area: the public endpoint serves the global
  // config, so Save is disabled rather than accepting values nobody sees.
  locked = false;
  private lastLoaded: Setting[] = [];
  private snapshot = '';

  ownStats: OwnStatsState = 'loading';
  statsKeySet = false;
  /** The key field is open over a stored key (replace). */
  editingKey = false;
  keyInput = '';
  keyError = '';
  keySaving = false;
  /** Replaced (not mutated) after a key change, so the panel reloads. */
  statsSource = () => this.adminService.getMagnetStats();

  @ViewChild('keyField') private keyField?: ElementRef<HTMLInputElement>;

  constructor(
    private adminService: AdminService,
    private authService: AuthService,
    private magnetAdsService: MagnetAdsService,
    private confirm: ConfirmService,
    private toast: NbToastrService,
  ) {}

  get isSuperAdmin(): boolean {
    return this.authService.userInfo?.globalRole === 'super_admin';
  }

  get dirty(): boolean {
    return this.loaded && JSON.stringify(this.buildPayload()) !== this.snapshot;
  }

  /** On, but nothing to show: the toggle alone does not make an ad appear. */
  get enabledWithoutSnippet(): boolean {
    return this.enabled && !this.snippet?.trim();
  }

  ngOnInit(): void {
    this.loadSettings();
    // The public magnet endpoint is the only place the lock is visible to an
    // owner; forced so a stale cached answer from before the lock is not used.
    this.magnetAdsService.loadSettings(true)
      .then(s => this.locked = this.locked || !!s?.locked);
    this.loadOwnStats();
  }

  loadSettings() {
    this.loadFailed = false;
    this.loading = true;
    this.adminService.getSettings()
      .then(settings => {
        this.lastLoaded = settings || [];
        this.load(this.lastLoaded);
        this.loaded = true;
        this.snapshot = JSON.stringify(this.buildPayload());
      })
      .catch((err) => {
        this.loadFailed = true;
        if (err?.status === 403 || err?.status === 401) {
          this.accessDenied.emit();
          return;
        }
        this.toast.danger('', 'לא הצלחנו לטעון את ההגדרות — נסו שוב');
      })
      .finally(() => this.loading = false);
  }

  resetChanges() {
    this.load(this.lastLoaded);
  }

  private load(settings: Setting[]) {
    this.otherSettings = [];
    const known = new Set<string>(MAGNET_KEYS);
    this.enabled = false;
    this.snippet = '';
    this.mode = 'by_messages';
    this.perMessages = 5;
    this.minTimeSeconds = 0;
    this.perSeconds = 60;
    this.minMessagesSince = 0;

    for (const s of settings) {
      if (!known.has(s.key)) {
        this.otherSettings.push(s);
        continue;
      }
      const v = s.value;
      switch (s.key) {
        case 'magnet_enabled':
          this.enabled = toBool(v);
          break;
        case 'magnet_snippet':
          this.snippet = v == null ? '' : String(v);
          break;
        case 'magnet_mode':
          this.mode = (String(v) === 'by_time' ? 'by_time' : 'by_messages');
          break;
        case 'magnet_per_messages':
          this.perMessages = this.toInt(v, 5);
          break;
        case 'magnet_min_time_seconds':
          this.minTimeSeconds = this.toInt(v, 0);
          break;
        case 'magnet_per_seconds':
          this.perSeconds = this.toInt(v, 60);
          break;
        case 'magnet_min_messages_since':
          this.minMessagesSince = this.toInt(v, 0);
          break;
      }
    }
  }

  private toInt(v: any, fallback: number): number {
    if (v === null || v === undefined || v === '') return fallback;
    const n = parseInt(String(v), 10);
    return isNaN(n) ? fallback : n;
  }

  private buildPayload(): Setting[] {
    const out: Setting[] = [...this.otherSettings];

    if (this.enabled) out.push({ key: 'magnet_enabled', value: '1' as any });
    if (this.snippet?.trim()) out.push({ key: 'magnet_snippet', value: this.snippet as any });
    out.push({ key: 'magnet_mode', value: this.mode as any });

    if (this.mode === 'by_messages') {
      if (this.perMessages > 0) out.push({ key: 'magnet_per_messages', value: String(this.perMessages) as any });
      if (this.minTimeSeconds > 0) out.push({ key: 'magnet_min_time_seconds', value: String(this.minTimeSeconds) as any });
    } else {
      if (this.perSeconds > 0) out.push({ key: 'magnet_per_seconds', value: String(this.perSeconds) as any });
      if (this.minMessagesSince > 0) out.push({ key: 'magnet_min_messages_since', value: String(this.minMessagesSince) as any });
    }
    return out;
  }

  save() {
    if (!this.loaded) return;
    this.inProgress = true;
    const out = this.buildPayload();

    this.adminService.setSettings(out)
      .then(() => {
        this.toast.success('', 'הגדרות הפרסומות נשמרו');
        this.lastLoaded = out;
        this.snapshot = JSON.stringify(out);
      })
      .catch((err) => this.toast.danger('', err?.status === 403 || err?.status === 401
        ? 'רק בעלי הערוץ יכולים לשנות את הגדרות הפרסומות'
        : 'שמירת ההגדרות נכשלה — נסו שוב'))
      .finally(() => this.inProgress = false);
  }

  // ─── The channel's own Magnet numbers ───────────────────────────────────

  /** Asks the owner route, not the public endpoint: the server alone decides the lock. */
  loadOwnStats() {
    this.ownStats = 'loading';
    this.adminService.getMagnetStatsKey()
      .then(r => {
        this.statsKeySet = !!r?.statsKeySet;
        this.editingKey = false;
        this.ownStats = 'ready';
      })
      .catch(err => {
        if (this.isLockedError(err)) {
          this.showLocked();
          return;
        }
        this.ownStats = 'error';
      });
  }

  startKeyEdit() {
    this.editingKey = true;
    this.keyError = '';
    setTimeout(() => this.keyField?.nativeElement.focus());
  }

  cancelKeyEdit() {
    this.editingKey = false;
    this.keyInput = '';
    this.keyError = '';
  }

  saveKey() {
    const key = this.keyInput.trim();
    if (!STATS_KEY_RE.test(key)) {
      this.keyError = 'זה לא נראה כמו מפתח סטטיסטיקה: הוא מתחיל ב-mgs_ ואחריו 48 תווים (ספרות ואותיות a–f). העתיקו אותו שוב ממגנט.';
      return;
    }
    this.writeKey({ statsKey: key }, 'מפתח הסטטיסטיקה נשמר');
  }

  async clearKey() {
    const ok = await this.confirm.ask({
      title: 'למחוק את מפתח הסטטיסטיקה?',
      message: 'הנתונים ממגנט לא יוצגו כאן עד שתדביקו מפתח חדש. הפרסומות עצמן ממשיכות להופיע כרגיל.',
      status: 'danger',
      confirmLabel: 'מחיקה',
    });
    if (ok) this.writeKey({ clearStatsKey: true }, 'מפתח הסטטיסטיקה נמחק');
  }

  private writeKey(body: { statsKey?: string; clearStatsKey?: boolean }, done: string) {
    this.keySaving = true;
    this.keyError = '';
    this.adminService.setMagnetStatsKey(body)
      .then(r => {
        this.statsKeySet = !!r?.statsKeySet;
        this.keyInput = '';
        this.editingKey = false;
        this.statsSource = () => this.adminService.getMagnetStats();
        this.toast.success('', done);
      })
      .catch(err => {
        if (this.isLockedError(err)) {
          this.showLocked();
          return;
        }
        if (err?.error?.error === 'invalid_stats_key') {
          this.keyError = err.error.message || 'מפתח הסטטיסטיקה אינו תקין';
          return;
        }
        this.toast.danger('', err?.status === 403 || err?.status === 401
          ? 'רק בעלי הערוץ יכולים לשנות את מפתח הסטטיסטיקה'
          : 'שמירת המפתח נכשלה — נסו שוב');
      })
      .finally(() => this.keySaving = false);
  }

  private isLockedError(err: any): boolean {
    return err?.status === 403 && err?.error?.error === 'magnet_locked';
  }

  private showLocked() {
    this.ownStats = 'locked';
    this.locked = true;
    this.statsKeySet = false;
    this.keyInput = '';
    this.editingKey = false;
  }
}
