import { Component, OnInit } from '@angular/core';
import { NbCardModule } from "@nebular/theme";
import { AdminService } from '../../../services/admin.service';
import { Statistics } from '../../../models/statistics.model';
import { MessageTimePipe } from '../../../pipes/message-time.pipe';

@Component({
  selector: 'app-statistics',
  imports: [
    NbCardModule,
    MessageTimePipe
  ],
  templateUrl: './statistics.component.html',
  styleUrl: './statistics.component.scss'
})
export class StatisticsComponent implements OnInit {
  statistics: Statistics | null = null;

  constructor(
    private adminService: AdminService
  ) { }

  ngOnInit(): void {
    this.adminService.getStatistics().then(statistics => {
      this.statistics = statistics;
    });
  }
}
