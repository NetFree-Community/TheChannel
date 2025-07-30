import { Component, OnInit } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NbCardModule, NbLayoutModule } from "@nebular/theme";
import { NotificationsService } from './services/notifications.service';

@Component({
  selector: 'app-root',
  imports: [
    RouterOutlet,
    NbLayoutModule,
    NbCardModule,
],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss'
})
export class AppComponent implements OnInit {

  constructor(
    private notificationsService: NotificationsService,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();
  }
}
