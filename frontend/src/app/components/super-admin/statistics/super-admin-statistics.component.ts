import { Component, EventEmitter, OnDestroy, OnInit, Output } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';
import { ConfirmService } from '../../../services/confirm.service';
import { magnetStatsError } from '../../../services/magnet-stats-errors';
import {
  MagnetAdRow,
  MagnetDay,
  MagnetRange,
  MagnetStatsView,
  fmtAgo,
  fmtDayLong,
  fmtDayShort,
  fmtInt,
  fmtMoney,
  fmtMoneyShort,
  fmtPct,
  niceMax,
  toMagnetStatsView,
} from '../../../services/magnet-stats.model';

type ChartMetric = 'earnings' | 'clicks' | 'impressions';

interface ChartBar {
  day: MagnetDay;
  value: number | null;
  /** 0..100, the bar's height as a share of the axis. */
  pct: number;
  inRange: boolean;
  label: string;
}

const RANGES: { id: MagnetRange; label: string; days: number }[] = [
  { id: 'today', label: 'היום', days: 1 },
  { id: 'week', label: '7 ימים', days: 7 },
  { id: 'month', label: '30 יום', days: 30 },
];

const METRICS: { id: ChartMetric; label: string }[] = [
  { id: 'earnings', label: 'רווח' },
  { id: 'clicks', label: 'קליקים' },
  { id: 'impressions', label: 'חשיפות' },
];

const DEVICE_LABELS: { id: 'mobile' | 'desktop' | 'tablet' | 'unknown'; label: string; icon: string }[] = [
  { id: 'mobile', label: 'נייד', icon: 'smartphone-outline' },
  { id: 'desktop', label: 'מחשב', icon: 'monitor-outline' },
  { id: 'tablet', label: 'טאבלט', icon: 'browser-outline' },
  { id: 'unknown', label: 'לא ידוע', icon: 'question-mark-circle-outline' },
];

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
export class SuperAdminStatisticsComponent implements OnInit, OnDestroy {
  /** The key is fixed on the "פרסומות מגנט" screen; the panel switches to it. */
  @Output() openMagnet = new EventEmitter<void>();

  stats: MagnetStatsView | null = null;
  loadingStats = true;
  resetting = false;
  /** Shown in the magnet card instead of the numbers. */
  magnetError = '';
  /** True when the key is simply not set up — a setup hint, not a failure. */
  magnetNotConfigured = false;
  /** The fix is entering or replacing the key. */
  magnetKeyProblem = false;

  readonly ranges = RANGES;
  readonly metrics = METRICS;
  range: MagnetRange = 'month';
  metric: ChartMetric = 'earnings';

  /** Computed once per answer / selection, not on every change detection. */
  bars: ChartBar[] = [];
  axisMax = 0;
  axisTicks: { pct: number; label: string }[] = [];
  xLabels: { index: number; label: string }[] = [];
  hoverIndex: number | null = null;
  adRows: (MagnetAdRow & { share: number })[] = [];
  hourBars: { hour: number; clicks: number; pct: number }[] = [];
  peakHour: number | null = null;
  devices: { id: string; label: string; icon: string; clicks: number; share: number }[] = [];
  placements: { name: string; clicks: number; earnings: number; share: number }[] = [];

  updatedAgo = '';
  private agoTimer?: ReturnType<typeof setInterval>;

  readonly fmtInt = fmtInt;
  readonly fmtMoney = fmtMoney;
  readonly fmtPct = fmtPct;
  readonly fmtDayLong = fmtDayLong;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
  ) {}

  ngOnInit(): void {
    this.loadMagnetStats();
    // "עודכן לפני X דקות" keeps moving while the screen stays open.
    this.agoTimer = setInterval(() => this.refreshAgo(), 30_000);
  }

  ngOnDestroy(): void {
    if (this.agoTimer) clearInterval(this.agoTimer);
  }

  loadMagnetStats() {
    this.loadingStats = true;
    this.magnetError = '';
    this.magnetNotConfigured = false;
    this.magnetKeyProblem = false;
    this.superAdminService.getMagnetStats()
      .then(raw => {
        this.stats = toMagnetStatsView(raw);
        this.rebuild();
      })
      .catch(err => {
        // A missing key is the normal state of a platform that never used
        // Magnet, not an outage: a setup hint here, and no warning toast.
        const { message, notConfigured, keyProblem } = magnetStatsError(err);
        this.stats = null;
        this.magnetError = message;
        this.magnetNotConfigured = notConfigured;
        this.magnetKeyProblem = keyProblem;
        if (!notConfigured) this.toastr.warning('', 'הנתונים ממגנט לא נטענו');
      })
      .finally(() => this.loadingStats = false);
  }

  // ─── What the template reads ────────────────────────────────────────────

  get current() {
    return this.stats?.summary[this.range] ?? null;
  }

  get rangeLabel(): string {
    switch (this.range) {
      case 'today': return 'היום';
      case 'week': return 'ב-7 הימים האחרונים';
      default: return 'ב-30 הימים האחרונים';
    }
  }

  /**
   * Impressions are kept 7 days, so the 30-day view shows the 7-day figure
   * and says so, instead of a blank card.
   */
  get impressionsCard(): { value: number; ctr: number | null; note: string } | null {
    const s = this.stats;
    if (!s) return null;
    const cur = s.summary[this.range];
    if (cur.impressions !== null) return { value: cur.impressions, ctr: cur.ctr, note: this.rangeLabel };
    const week = s.summary.week;
    if (this.range === 'month' && week.impressions !== null) {
      return { value: week.impressions, ctr: week.ctr, note: `ב-${s.impressionsDays} הימים האחרונים · נשמרות ${s.impressionsDays} ימים` };
    }
    return null;
  }

  get hasClicksThisMonth(): boolean {
    return (this.stats?.summary.month.clicks ?? 0) > 0;
  }

  get hasDaily(): boolean {
    return !!this.stats?.daily?.length;
  }

  get metricHasData(): boolean {
    return this.bars.some(b => (b.value ?? 0) > 0);
  }

  get hoverBar(): ChartBar | null {
    return this.hoverIndex === null ? null : this.bars[this.hoverIndex] ?? null;
  }

  /** The tooltip's horizontal anchor, kept inside the chart. */
  get hoverLeft(): number {
    if (this.hoverIndex === null || !this.bars.length) return 50;
    const center = ((this.hoverIndex + 0.5) / this.bars.length) * 100;
    return Math.min(85, Math.max(15, center));
  }

  setRange(r: MagnetRange) {
    this.range = r;
    this.buildChart();
  }

  setMetric(m: ChartMetric) {
    this.metric = m;
    this.hoverIndex = null;
    this.buildChart();
  }

  formatMetric(v: number | null, metric: ChartMetric = this.metric): string {
    if (v === null) return 'לא נשמר';
    return metric === 'earnings' ? fmtMoney(v) : fmtInt(v);
  }

  hourLabel(h: number): string {
    return `${String(h).padStart(2, '0')}:00`;
  }

  // ─── Derived data ───────────────────────────────────────────────────────

  private rebuild() {
    this.refreshAgo();
    this.buildChart();
    this.buildBreakdowns();
  }

  private refreshAgo() {
    const at = this.stats?.generatedAt;
    this.updatedAgo = at ? fmtAgo(at, new Date()) : '';
  }

  private buildChart() {
    const daily = this.stats?.daily;
    if (!daily?.length) {
      this.bars = [];
      this.axisTicks = [];
      this.xLabels = [];
      return;
    }
    const metric = this.metric;
    const rangeDays = RANGES.find(r => r.id === this.range)?.days ?? 30;
    const values = daily.map(d => metric === 'impressions' ? d.impressions : d[metric]);
    const max = niceMax(Math.max(0, ...values.map(v => v ?? 0)));
    this.axisMax = max;
    this.bars = daily.map((day, i) => ({
      day,
      value: values[i],
      pct: max > 0 && values[i] ? Math.max(1.5, ((values[i] as number) / max) * 100) : 0,
      inRange: i >= daily.length - rangeDays,
      label: `${fmtDayLong(day.date)}: ${this.formatMetric(values[i], metric)}`,
    }));
    this.axisTicks = max > 0
      ? [1, 0.5].map(f => ({
          pct: f * 100,
          label: metric === 'earnings' ? fmtMoneyShort(max * f) : fmtInt(max * f),
        }))
      : [];
    // A date under every 7th bar, counted back from today so today is labelled.
    this.xLabels = daily
      .map((d, i) => ({ index: i, label: fmtDayShort(d.date) }))
      .filter(x => (daily.length - 1 - x.index) % 7 === 0);
  }

  private buildBreakdowns() {
    const s = this.stats;
    const ads = s?.byAd ?? [];
    const maxEarn = Math.max(0, ...ads.map(a => a.earnings));
    const maxClicks = Math.max(0, ...ads.map(a => a.clicks));
    this.adRows = ads.map(a => ({
      ...a,
      share: maxEarn > 0 ? (a.earnings / maxEarn) * 100 : maxClicks > 0 ? (a.clicks / maxClicks) * 100 : 0,
    }));

    const hours = s?.byHour ?? [];
    const hourMax = Math.max(0, ...hours);
    this.hourBars = hours.map((clicks, hour) => ({
      hour,
      clicks,
      pct: hourMax > 0 && clicks > 0 ? Math.max(3, (clicks / hourMax) * 100) : 0,
    }));
    this.peakHour = hourMax > 0 ? hours.indexOf(hourMax) : null;

    const dev = s?.byDevice;
    const devTotal = dev ? dev.mobile + dev.desktop + dev.tablet + dev.unknown : 0;
    this.devices = dev && devTotal > 0
      ? DEVICE_LABELS
          .map(d => ({ ...d, clicks: dev[d.id], share: (dev[d.id] / devTotal) * 100 }))
          .filter(d => d.clicks > 0)
      : [];

    const pl = s?.byPlacement ?? [];
    const plMax = Math.max(0, ...pl.map(p => p.earnings));
    this.placements = pl.map(p => ({ ...p, share: plMax > 0 ? (p.earnings / plMax) * 100 : 0 }));
  }

  /** The whole series, newest first, for the table behind the chart. */
  get tableDays(): MagnetDay[] {
    return this.stats?.daily ? [...this.stats.daily].reverse() : [];
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
}
