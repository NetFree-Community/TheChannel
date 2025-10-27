import { Component } from '@angular/core';
import { NbButtonModule, NbCardModule, NbDatepickerModule, NbDialogRef, NbInputModule } from '@nebular/theme';

@Component({
  selector: 'app-time-picker',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbDatepickerModule,
  ],
  templateUrl: './time-picker.component.html',
  styleUrl: './time-picker.component.scss'
})
export class TimePickerComponent {

  constructor(
    private dialogRef: NbDialogRef<TimePickerComponent>
  ) { }

  date: Date | null = null;

  timeChange(event: Date) {
    this.date = event;
  }

  close(ok: boolean = false) {
    ok ? this.dialogRef.close(this.date) : this.dialogRef.close();
  }
}
