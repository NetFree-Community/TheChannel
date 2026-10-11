import {
  AfterViewInit,
  Component,
  ElementRef,
  Input,
  NgZone,
  OnDestroy,
  ViewChild,
} from '@angular/core';
import { MagnetAdsService } from '../../../../services/magnet-ads.service';
import { ChatService } from '../../../../services/chat.service';

/**
 * The frame's content script posts this after every layout change so the
 * host can size the frame to the ad. Its origin is opaque ("null"), so the
 * host matches the message by its source window, never by origin.
 */
const AD_SIZE_MESSAGE = 'magnet-ad-size';

/** Height the frame starts at, so an ad script sees a viewport to render into. */
const INITIAL_FRAME_HEIGHT = 250;

/**
 * Time the snippet gets, once its frame has loaded, to render something
 * before the slot is hidden. It counts from the frame's load event rather
 * than from render, so an ad network that answers slowly does not lose its
 * slot to the clock.
 */
const EMPTY_SLOT_GRACE_MS = 5000;

/** Hard ceiling from render, for a frame whose load event never fires. */
const EMPTY_SLOT_MAX_WAIT_MS = 20000;

/**
 * Runs first inside the frame. Without allow-same-origin the frame's origin
 * is opaque, so reading localStorage, sessionStorage or document.cookie
 * throws a SecurityError. An ad script that touches them outside a
 * try/catch would die before rendering, so each one is replaced by an
 * in-memory stand-in that lives as long as the frame.
 */
const STORAGE_SHIM = `(function(){` +
  `function mem(){var s={};return {getItem:function(k){return Object.prototype.hasOwnProperty.call(s,k)?s[k]:null},` +
  `setItem:function(k,v){s[k]=String(v)},removeItem:function(k){delete s[k]},clear:function(){s={}},` +
  `key:function(i){return Object.keys(s)[i]||null},get length(){return Object.keys(s).length}};}` +
  `['localStorage','sessionStorage'].forEach(function(n){try{void window[n];}catch(e){` +
  `try{Object.defineProperty(window,n,{value:mem(),configurable:true});}catch(_){}}});` +
  `try{void document.cookie;}catch(e){try{Object.defineProperty(document,'cookie',` +
  `{get:function(){return '';},set:function(){},configurable:true});}catch(_){}}` +
  `})();`;

@Component({
  selector: 'app-magnet-ad-slot',
  standalone: true,
  imports: [],
  templateUrl: './magnet-ad-slot.component.html',
  styleUrl: './magnet-ad-slot.component.scss',
})
export class MagnetAdSlotComponent implements AfterViewInit, OnDestroy {
  @Input() slotKey: string = '';

  @ViewChild('host', { static: true }) hostRef!: ElementRef<HTMLDivElement>;

  private observer?: IntersectionObserver;
  private rendered = false;
  private collapseTimer?: any;
  private frame?: HTMLIFrameElement;
  private reportedHeight: number | null = null;
  private readonly onMessage = (event: MessageEvent) => this.handleMessage(event);
  collapsed = false;
  /** The super admin's global snippet is shown instead of the channel's own. */
  locked = false;

  constructor(
    private magnet: MagnetAdsService,
    public chatService: ChatService,
    private zone: NgZone,
  ) {}

  ngAfterViewInit(): void {
    if (typeof IntersectionObserver === 'undefined') {
      this.render();
      return;
    }

    this.observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && !this.rendered) {
            this.render();
            this.observer?.disconnect();
            break;
          }
        }
      },
      { rootMargin: '200px 0px' },
    );
    this.observer.observe(this.hostRef.nativeElement);
  }

  ngOnDestroy(): void {
    this.observer?.disconnect();
    if (this.collapseTimer) clearTimeout(this.collapseTimer);
    window.removeEventListener('message', this.onMessage);
  }

  private render(): void {
    if (this.rendered) return;
    this.rendered = true;

    const settings = this.magnet.getSettings();
    this.locked = !!settings?.locked;
    const snippet = settings?.snippet?.trim();
    if (!snippet) {
      this.collapse();
      return;
    }

    const host = this.hostRef.nativeElement;
    host.innerHTML = '';

    // The snippet is channel-owner content, and any signed-in user can own a
    // channel. It used to be injected into this document with its scripts
    // re-created, so it ran as first-party code on the app's origin, with
    // every viewer's session: an owner could read the super admin's global
    // settings or grant themselves roles when the super admin opened the
    // channel. A sandboxed srcdoc frame (no allow-same-origin) gives the
    // snippet an opaque origin: its script still runs and its ad still
    // renders, but it cannot reach this document, the session cookie or
    // the API.
    const frame = document.createElement('iframe');
    frame.className = 'magnet-ad-frame';
    frame.setAttribute('sandbox', 'allow-scripts allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation');
    frame.setAttribute('referrerpolicy', 'strict-origin-when-cross-origin');
    frame.setAttribute('title', 'פרסומת');
    frame.setAttribute('scrolling', 'no');
    frame.style.width = '100%';
    frame.style.height = `${INITIAL_FRAME_HEIGHT}px`;
    frame.style.border = '0';
    frame.style.display = 'block';
    frame.srcdoc = this.frameDocument(snippet);

    this.frame = frame;
    window.addEventListener('message', this.onMessage);
    frame.addEventListener('load', () => this.armCollapse(EMPTY_SLOT_GRACE_MS), { once: true });
    host.appendChild(frame);
    this.armCollapse(EMPTY_SLOT_MAX_WAIT_MS);
  }

  /** (Re)starts the empty-slot clock; the latest arming wins. */
  private armCollapse(delay: number): void {
    if (this.collapseTimer) clearTimeout(this.collapseTimer);
    this.collapseTimer = setTimeout(() => this.collapseIfEmpty(), delay);
  }

  /**
   * The document the frame runs. The base href keeps relative URLs in the
   * snippet resolving against this site, as they did inline, and target
   * _blank sends ad clicks to a new tab, since the sandbox blocks
   * navigating this one without a user gesture. The trailing script
   * reports the rendered height so the host can size the frame.
   */
  private frameDocument(snippet: string): string {
    const base = `${location.origin}/`;
    return `<!doctype html><html lang="he" dir="rtl"><head><meta charset="utf-8">` +
      `<base href="${base}" target="_blank">` +
      `<style>html,body{margin:0;padding:0;background:transparent;overflow:hidden}</style>` +
      `<script>${STORAGE_SHIM}</script>` +
      `</head><body>${snippet}` +
      `<script>(function(){` +
      `var last=-1;` +
      `function h(){var b=document.body;return Math.ceil(Math.max(b?b.scrollHeight:0,b?b.offsetHeight:0));}` +
      `function report(){var v=h();if(v!==last){last=v;parent.postMessage({type:${JSON.stringify(AD_SIZE_MESSAGE)},height:v},'*');}}` +
      `if(window.ResizeObserver){var ro=new ResizeObserver(report);ro.observe(document.documentElement);if(document.body)ro.observe(document.body);}` +
      `new MutationObserver(report).observe(document.documentElement,{childList:true,subtree:true,attributes:true});` +
      `window.addEventListener('load',report);setInterval(report,1000);report();` +
      `})();</script></body></html>`;
  }

  private handleMessage(event: MessageEvent): void {
    // Only this slot's own frame may size it. The frame's origin is "null",
    // so the source window is the only reliable identity.
    if (!this.frame || event.source !== this.frame.contentWindow) return;
    const data = event.data;
    if (!data || data.type !== AD_SIZE_MESSAGE) return;
    const height = Number(data.height);
    if (!Number.isFinite(height) || height < 0) return;

    const hadContent = !!this.reportedHeight;
    this.reportedHeight = height;
    if (height > 0) {
      // Cap what an ad may grow to: the slot sits inside the chat, not over it.
      this.frame.style.height = `${Math.min(height, 1200)}px`;
      if (this.collapsed) {
        // The ad arrived after the clock hid the slot: show it again.
        this.zone.run(() => { this.collapsed = false; });
      }
    } else if (hadContent) {
      // The ad emptied itself (no campaign, closed by the viewer). Give it
      // the same grace as at start before hiding the slot, since a script
      // replacing its content can pass through zero on the way.
      this.armCollapse(EMPTY_SLOT_GRACE_MS);
    }
  }

  private collapseIfEmpty(): void {
    // No report yet, or a reported height of zero: the snippet rendered
    // nothing visible, so the slot is hidden instead of leaving a gap. The
    // frame stays alive and laid out (see the collapsed style), so an ad
    // that renders later can reopen the slot with its size report.
    if (!this.reportedHeight && !this.collapsed) {
      this.zone.run(() => { this.collapsed = true; });
    }
  }

  /** Nothing to render at all: hide the slot and drop the frame. */
  private collapse(): void {
    this.collapsed = true;
    window.removeEventListener('message', this.onMessage);
    this.frame = undefined;
    const host = this.hostRef.nativeElement;
    host.innerHTML = '';
  }
}
