import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbDialogRef, NbCardModule, NbButtonModule, NbInputModule, NbToastrService } from '@nebular/theme';
import { ChatService } from '../../../../../services/chat.service';

@Component({
  selector: 'app-report',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    FormsModule
  ],
  templateUrl: './report.component.html',
  styleUrl: './report.component.scss'
})
export class ReportComponent implements OnInit{
  messageId: number | undefined;
  reason: string = '';

  constructor(
    public dialogRef: NbDialogRef<ReportComponent>,
    private chatService: ChatService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit() {
    this.messageId = this.dialogRef.componentRef.instance.messageId;
  }

  reportMessage() {
    if (!this.messageId || !this.reason.trim()) {
      return;
    }

    this.chatService.reportMessage(this.messageId, this.reason)
      .then(() => {
        this.toastrService.success('', 'הדיווח נשלח בהצלחה!');
        this.dialogRef.close();
      })
      .catch(() => this.toastrService.danger('', 'אירעה שגיאה בעת שליחת הדיווח'));
  }
}
