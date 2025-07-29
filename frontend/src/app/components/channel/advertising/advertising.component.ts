import { Component, Input, Renderer2, RendererStyleFlags2 } from '@angular/core';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';
import { Ad } from "../../../services/ads.service";

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
    this.sayfeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(ad.src);
    this.renderer.setStyle(document.getElementById('container'), '--ad-width', `${ ad.width }px`, RendererStyleFlags2.DashCase);
  }

  sayfeUrl: SafeResourceUrl = '';

  constructor(
    private sanitizer: DomSanitizer,
    private renderer: Renderer2,
  ) { }

}
