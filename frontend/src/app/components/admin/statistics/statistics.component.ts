import { Component, OnInit, ViewChild } from '@angular/core';
import { NbButtonModule, NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService } from '../../../services/admin.service';
import { Statistics } from '../../../models/statistics.model';
import { MessageTimePipe } from '../../../pipes/message-time.pipe';
import { BaseChartDirective } from 'ng2-charts';
import { Chart, ChartConfiguration } from 'chart.js';
import zoomPlugin from 'chartjs-plugin-zoom';

Chart.register(zoomPlugin);

@Component({
  selector: 'app-statistics',
  imports: [
    NbCardModule,
    MessageTimePipe,
    BaseChartDirective,
    NbButtonModule
  ],
  templateUrl: './statistics.component.html',
  styleUrl: './statistics.component.scss'
})
export class StatisticsComponent implements OnInit {
  statistics: Statistics | null = null;
  lineChartData: ChartConfiguration['data'] = {
    datasets: [
      {
        data: [],
        label: 'סטטיסטקת חיבורים לימים אחרונים',
        backgroundColor: 'rgba(148,159,177,0.2)',
        borderColor: 'rgba(148,159,177,1)',
        pointBackgroundColor: 'rgba(148,159,177,1)',
        pointBorderColor: '#fff',
        pointHoverBackgroundColor: '#fff',
        pointHoverBorderColor: 'rgba(148,159,177,0.8)',
        fill: 'origin',
      },
    ],
    labels: [],
  }
  lineChartOptions: ChartConfiguration['options'] = {
    responsive: true,
    plugins: {
      zoom: {
        zoom: {
          wheel: {
            enabled: true,
          },
          pinch: {
            enabled: true,
          },
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
    private tostService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.updateInfo();
  }

  resetPeakCounting() {
    this.adminService.resetPeakCounting().then(() => {
      this.updateInfo();
      this.tostService.success("", "המונה אופס בהצלחה!");
    }).catch(() =>
      this.tostService.danger("שגיאה באיפוס המונה")
    );
  }

  updateInfo() {
    this.adminService.getStatistics().then(statistics => {
      this.statistics = statistics;
      this.lineChartData.datasets[0].data = statistics.connectionsStatistics.date;
      this.lineChartData.labels = statistics.connectionsStatistics.labels;
      this.chart?.update();
    });
  }
}
