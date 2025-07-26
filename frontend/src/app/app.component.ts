import { Component, OnInit, HostListener } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NbCardModule, NbLayoutModule } from "@nebular/theme";
import { NotificationsService } from './services/notifications.service';
import { SoundService } from './services/sound.service';

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
  title = 'channel';
  private hasUserInteracted = false;

  constructor(
    private notificationsService: NotificationsService,
    private soundService: SoundService,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();
  }

  @HostListener('document:click', ['$event'])
  @HostListener('document:keydown', ['$event'])
  async onUserInteraction() {
    if (!this.hasUserInteracted && this.soundService.isEnabled()) {
      this.hasUserInteracted = true;
      await this.soundService.initializeAudioContext();
    }
  }
}