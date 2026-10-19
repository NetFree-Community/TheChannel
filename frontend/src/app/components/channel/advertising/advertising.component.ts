import { Component, Input, Renderer2, RendererStyleFlags2 } from '@angular/core';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';
import { Ad } from "../../../services/ads.service";

/**
 * Only an absolute http(s) URL may be framed. The value is channel-owner
 * input; bypassSecurityTrustResourceUrl on a javascript: URL would run the
 * owner's script in this document's origin, with every viewer's session.
 */
export function isFramableUrl(src: string | undefined | null): boolean {
  if (!src) return false;
  try {
    // Absolute only, as the server requires; a relative value is not a URL
    // the owner was ever offered. A protocol-relative one (//host/path) is
    // what some existing channels saved and resolves to the page's scheme.
    const url = new URL(src.startsWith('//') ? 'https:' + src : src);
    return (url.protocol === 'https:' || url.protocol === 'http:') && !!url.host;
  } catch {
    return false;
  }
}

@Component({
  selector: 'app-advertising',
  imports: [],
  templateUrl: './advertising.component.html',
  styleUrl: './advertising.component.scss'
})
export class AdvertisingComponent {

  _ad?: Ad;

  get ad(): Ad | undefined {
    return this._ad;
  }

  @Input()
  set ad(ad: Ad) {
    this._ad = ad;
    this.sayfeUrl = isFramableUrl(ad?.src) ? this.sanitizer.bypassSecurityTrustResourceUrl(ad.src) : null;
    this.renderer.setStyle(document.getElementById('container'), '--ad-width', `${ ad.width }px`, RendererStyleFlags2.DashCase);
  }

  sayfeUrl: SafeResourceUrl | null = null;

  constructor(
    private sanitizer: DomSanitizer,
    private renderer: Renderer2,
  ) { }

}
