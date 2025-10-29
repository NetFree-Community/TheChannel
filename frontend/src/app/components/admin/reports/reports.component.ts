import { Component, Input, OnInit } from '@angular/core';
import { NbButtonModule, NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService } from '../../../services/admin.service';
import { Reports, Report } from '../../../models/report.model';

import { MessageTimePipe } from "../../../pipes/message-time.pipe";

@Component({
  selector: 'app-reports',
  imports: [
    NbCardModule,
    NbButtonModule,
    MessageTimePipe
],
  templateUrl: './reports.component.html',
  styleUrl: './reports.component.scss'
})
export class ReportsComponent implements OnInit {

  reports: Reports = [];
  @Input() status: 'open' | 'closed' | 'all' = 'open';

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit(): void {
    this.adminService.getReports(this.status).then(reports => {
      this.reports = reports;
    })
      .catch(() => this.toastrService.danger('', 'אירעה שגיאה בעת טעינת הדיווחים'));
  }

  toggleReport(report: Report) {
    report.closed = !report.closed;
    report.updatedAt = new Date();
    this.adminService.setReports(report).then(() => {
      this.toastrService.success('', report.closed ? 'דיווח נסגר בהצלחה' : 'דיווח נפתח מחדש');
      this.status === 'all' ? this.reports[this.reports.findIndex(r => r.id === report.id)] = report : this.reports = this.reports.filter(r => r.id !== report.id);
    }).catch(() => this.toastrService.danger('', ''));
  }

  viewReport(messageId: number) {
    window.open(`${window.location.origin}/#${messageId}`, '_blank');
  }
}
