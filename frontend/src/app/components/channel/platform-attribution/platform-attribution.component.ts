import { Component, ElementRef, HostListener, Input, ViewChild } from '@angular/core';
import { RouterLink } from '@angular/router';
import { NbIconModule, NbPopoverDirective, NbPopoverModule } from '@nebular/theme';

/** The three forms of the credit, widest first. */
export type CreditVariant = 'full' | 'short' | 'badge';

/**
 * The platform credit ("הוקם ומתופעל ע״י הערוץ · ללא עלות") and its call to
 * action, drawn inside the channel header.
 *
 * It used to be a strip of its own under the header. The header now carries it
 * on the channel name's line, in whichever form fits the room the name leaves
 * (ChannelHeaderComponent measures and picks):
 *   - 'full'  — one pill with both halves of the sentence, the whole pill a link;
 *   - 'short' — the same pill reduced to the brand mark and the offer;
 *   - 'badge' — a small brand mark on the logo's corner that opens a card with
 *               the full sentence and the link. It takes no width at all.
 *
 * The call to action points at '/' (the landing page) on purpose:
 * a logged-out visitor — the audience for this credit — lands on the marketing
 * page with the Google sign-in CTA, and a signed-in reader without a channel is
 * bounced by LandingPageComponent to /channel, where the creation form waits.
 */
@Component({
  selector: 'app-platform-attribution',
  standalone: true,
  imports: [RouterLink, NbIconModule, NbPopoverModule],
  templateUrl: './platform-attribution.component.html',
  styleUrl: './platform-attribution.component.scss',
})
export class PlatformAttributionComponent {
  @Input() variant: CreditVariant = 'full';

  @ViewChild('pop') private pop?: NbPopoverDirective;

  /** The badge's card is open, for aria-expanded. */
  shown = false;

  constructor(private host: ElementRef<HTMLElement>) { }

  /**
   * The pill's width on one line, whether or not it is the form on show. The
   * pill never wraps (nowrap inline-flex), so its box is its max-content
   * width even inside the zero-width host of a form that is not in use.
   */
  naturalWidth(): number {
    return this.host.nativeElement.querySelector('.credit')?.getBoundingClientRect().width ?? 0;
  }

  // Nebular's click-triggered popover closes on an outside click only.
  @HostListener('document:keydown.escape')
  onEscape() {
    if (this.shown) this.pop?.hide();
  }
}
