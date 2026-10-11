import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { DatePipe } from '@angular/common';
import { FormsModule } from '@angular/forms';
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
import { MagnetAdsService } from '../../../services/magnet-ads.service';
import { SuperAdminService } from '../../../services/super-admin.service';
import { Setting } from '../../../models/setting.model';
import { toBool } from '../settings/settings.schema';

type MagnetMode = 'by_messages' | 'by_time';

interface MagnetStatsBucket {
  today: number;
  week: number;
  month: number;
}

interface MagnetStatsResponse {
  site?: { id?: string; domain?: string };
  currency?: string;
  clicks?: MagnetStatsBucket;
  earnings?: MagnetStatsBucket;
}

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
    DatePipe,
    FormsModule,
    NbAlertModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbRadioModule,
    NbSpinnerModule,
    NbTooltipModule,
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

  stats: MagnetStatsResponse | null = null;
  statsLoading = false;
  statsError = '';
  statsLoadedAt: Date | null = null;

  constructor(
    private adminService: AdminService,
    private authService: AuthService,
    private superAdminService: SuperAdminService,
    private magnetAdsService: MagnetAdsService,
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

  // Magnet returns a bare domain; bound to href as-is it resolved relative to
  // the admin page (/channel/<slug>/<domain>) and 404'd inside the app.
  siteUrl(domain: string | undefined): string {
    const d = (domain || '').trim();
    if (!d) return '';
    return /^https?:\/\//i.test(d) ? d : `https://${d}`;
  }

  ngOnInit(): void {
    this.loadSettings();
    // The public magnet endpoint is the only place the lock is visible to an
    // owner; forced so a stale cached answer from before the lock is not used.
    this.magnetAdsService.loadSettings(true)
      .then(s => this.locked = !!s?.locked);
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

  async loadStats() {
    this.statsLoading = true;
    this.statsError = '';

    try {
      this.stats = await this.superAdminService.getMagnetStats() as MagnetStatsResponse;
      this.statsLoadedAt = new Date();
    } catch (err: any) {
      const status = err?.status ?? 0;
      const data = err?.error;
      if (status === 400) {
        // The body is English ({"error":"missing_api_key","message":"Magnet
        // API key is not configured"}), and this tab has no key field, so
        // the text has to say where the key lives — as the super-admin
        // statistics card does.
        const code = typeof data === 'string' ? data : (data?.error ?? '');
        this.statsError = String(code).includes('missing_api_key')
          ? 'מפתח ה-API של מגנט לא הוגדר — הגדירו אותו בפאנל מנהל-על ← פרסומות מגנט'
          : 'מפתח ה-API של מגנט אינו תקין — בדקו אותו בפאנל מנהל-על ← פרסומות מגנט';
      } else if (status === 404) {
        this.statsError = 'האתר לא נמצא במערכת מגנט או שאינו מאושר.';
      } else if (status === 401 || status === 403) {
        this.statsError = 'אין הרשאה לגשת לנתוני מגנט.';
      } else if (status === 0) {
        this.statsError = 'שגיאת רשת בקריאה לשרת מגנט';
      } else {
        this.statsError = data?.message || `שגיאה בקבלת נתונים (HTTP ${status})`;
      }
      this.stats = null;
    } finally {
      this.statsLoading = false;
    }
  }

  formatMoney(n: number | undefined, currency: string | undefined): string {
    if (n === null || n === undefined) return '-';
    const code = (currency || 'ILS').toUpperCase();
    const symbol = code === 'ILS' ? '₪' : code === 'USD' ? '$' : code === 'EUR' ? '€' : code + ' ';
    const formatted = Number(n).toLocaleString('he-IL', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
    return code === 'ILS' ? `${formatted} ${symbol}` : `${symbol}${formatted}`;
  }

  formatNumber(n: number | undefined): string {
    if (n === null || n === undefined) return '-';
    return Number(n).toLocaleString('he-IL');
  }
}
