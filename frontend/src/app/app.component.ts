import { Component, OnInit, HostListener } from '@angular/core';
import { RouterOutlet, NavigationEnd, Router } from '@angular/router';
import { NbCardModule, NbLayoutModule } from "@nebular/theme";
import { NotificationsService } from './services/notifications.service';
import { SoundService } from './services/sound.service';
import { GoogleAnalyticsService } from './services/google-analytics.service';
import { filter } from 'rxjs';

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
    private googleAnalyticsService: GoogleAnalyticsService,
    private router: Router,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();
    this.googleAnalyticsService.init();

    this.router.events
      .pipe(filter(event => event instanceof NavigationEnd))
      .subscribe((event: NavigationEnd) => {
        this.googleAnalyticsService.trackPageView(event.urlAfterRedirects);
      });
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