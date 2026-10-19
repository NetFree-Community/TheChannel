import { Component, EventEmitter, OnInit, Output, ViewChild } from '@angular/core';
import { NbButtonModule, NbCardModule, NbIconModule, NbSpinnerModule, NbToastrService, NbTooltipModule } from "@nebular/theme";
import { AdminService } from '../../../services/admin.service';
import { Statistics } from '../../../models/statistics.model';
import { MessageTimePipe } from '../../../pipes/message-time.pipe';
import { BaseChartDirective } from 'ng2-charts';
import { Chart, ChartConfiguration } from 'chart.js';
import zoomPlugin from 'chartjs-plugin-zoom';

Chart.register(zoomPlugin);

/** Reads a theme token off the document, with the brand value as fallback. */
function cssToken(name: string, fallback: string): string {
  if (typeof getComputedStyle !== 'function') return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

@Component({
  selector: 'app-statistics',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbSpinnerModule,
    NbTooltipModule,
    MessageTimePipe,
    BaseChartDirective,
  ],
  templateUrl: './statistics.component.html',
  styleUrl: './statistics.component.scss'
})
export class StatisticsComponent implements OnInit {
  /** The statistics endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  statistics: Statistics | null = null;
  loading = true;
  loadFailed = false;

  lineChartData: ChartConfiguration['data'] = {
    datasets: [
      {
        data: [],
        label: 'חיבורים פתוחים',
        backgroundColor: cssToken('--color-primary-transparent-200', 'rgba(90, 95, 245, 0.16)'),
        borderColor: cssToken('--color-primary-500', '#5a5ff5'),
        pointBackgroundColor: cssToken('--color-primary-500', '#5a5ff5'),
        pointBorderColor: cssToken('--background-basic-color-1', '#fff'),
        pointHoverBackgroundColor: cssToken('--background-basic-color-1', '#fff'),
        pointHoverBorderColor: cssToken('--color-primary-700', '#3d41c4'),
        pointRadius: 2,
        pointHoverRadius: 5,
        borderWidth: 2,
        tension: 0.3,
        fill: 'origin',
      },
    ],
    labels: [],
  }

  lineChartOptions: ChartConfiguration['options'] = {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: 'index', intersect: false },
    scales: {
      x: {
        ticks: { maxTicksLimit: 8, maxRotation: 0, autoSkip: true },
        grid: { display: false },
      },
      y: {
        beginAtZero: true,
        ticks: { precision: 0 },
      },
    },
    plugins: {
      legend: { display: false },
      tooltip: { rtl: true, textDirection: 'rtl' },
      zoom: {
        zoom: {
          wheel: { enabled: true },
          pinch: { enabled: true },
          mode: 'x',
        },
        pan: {
          enabled: true,
          mode: 'x',
        }
      }
    }
  }
  @ViewChild(BaseChartDirective) chart?: BaseChartDirective;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  get hasSeries(): boolean {
    return (this.statistics?.connectionsStatistics?.labels?.length ?? 0) > 0;
  }

  ngOnInit(): void {
    this.updateData();
  }

  updateData() {
    this.loading = true;
    this.loadFailed = false;
    this.adminService.getStatistics().then(statistics => {
      this.statistics = statistics;
      // The server reads the series with ZREVRANGE, so index 0 is the newest
      // minute and, plotted as-is, the x-axis ran backwards in time. Reverse a
      // copy so the oldest point is on the left and pan/zoom follow the clock.
      const series = statistics.connectionsStatistics ?? { date: [], labels: [] };
      this.lineChartData.datasets[0].data = [...(series.date ?? [])].reverse();
      // Labels arrive as "02-01-2006 15:04"; the year is noise on a monthly
      // rolling window and the dashes read as minus signs in RTL text.
      this.lineChartData.labels = [...(series.labels ?? [])].reverse()
        .map(l => String(l).replace(/^(\d{2})-(\d{2})-\d{4} /, '$1.$2 '));
      this.chart?.update();
    }).catch((err) => {
      this.loadFailed = true;
      if (err?.status === 403 || err?.status === 401) {
        this.accessDenied.emit();
        return;
      }
      this.toastrService.danger('', 'לא הצלחנו לטעון את הסטטיסטיקות — נסו שוב');
    }).finally(() => this.loading = false);
  }

  resetZoom() {
    this.chart?.chart?.resetZoom();
  }
}
