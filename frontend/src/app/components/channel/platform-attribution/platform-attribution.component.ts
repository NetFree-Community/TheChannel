import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { NbIconModule } from '@nebular/theme';

/**
 * Thin attribution strip shown at the top of a channel page.
 *
 * The call to action points at '/' (the landing page) on purpose:
 * a logged-out visitor — the audience for this strip — lands on the marketing
 * page with the Google sign-in CTA, and a signed-in reader without a channel is
 * bounced by LandingPageComponent to /channel, where the creation form waits.
 */
@Component({
  selector: 'app-platform-attribution',
  standalone: true,
  imports: [RouterLink, NbIconModule],
  templateUrl: './platform-attribution.component.html',
  styleUrl: './platform-attribution.component.scss',
})
export class PlatformAttributionComponent { }
