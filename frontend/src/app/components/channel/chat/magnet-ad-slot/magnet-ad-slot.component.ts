import {
  AfterViewInit,
  Component,
  ElementRef,
  Input,
  OnDestroy,
  ViewChild,
} from '@angular/core';
import { NbUserModule } from '@nebular/theme';
import { MagnetAdsService } from '../../../../services/magnet-ads.service';
import { ChatService } from '../../../../services/chat.service';

@Component({
  selector: 'app-magnet-ad-slot',
  standalone: true,
  imports: [NbUserModule],
  templateUrl: './magnet-ad-slot.component.html',
  styleUrl: './magnet-ad-slot.component.scss',
})
export class MagnetAdSlotComponent implements AfterViewInit, OnDestroy {
  @Input() slotKey: string = '';

  @ViewChild('host', { static: true }) hostRef!: ElementRef<HTMLDivElement>;

  private observer?: IntersectionObserver;
  private rendered = false;
  private collapseTimer?: any;
  collapsed = false;

  constructor(
    private magnet: MagnetAdsService,
    public chatService: ChatService,
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
  }

  private render(): void {
    if (this.rendered) return;
    this.rendered = true;

    const settings = this.magnet.getSettings();
    const snippet = settings?.snippet?.trim();
    if (!snippet) {
      this.collapse();
      return;
    }

    const host = this.hostRef.nativeElement;
    host.innerHTML = '';

    const container = document.createElement('div');
    container.className = 'magnet-ad-content';
    host.appendChild(container);

    try {
      this.injectSnippet(container, snippet);
    } catch {
      this.collapse();
      return;
    }

    this.collapseTimer = setTimeout(() => this.collapseIfEmpty(), 5000);
  }

  private injectSnippet(container: HTMLElement, snippet: string): void {
    const template = document.createElement('template');
    template.innerHTML = snippet;

    const fragment = template.content;
    const scripts: HTMLScriptElement[] = [];
    fragment.querySelectorAll('script').forEach((s) => {
      scripts.push(s as HTMLScriptElement);
    });

    container.appendChild(fragment);

    for (const oldScript of scripts) {
      const newScript = document.createElement('script');
      for (const attr of Array.from(oldScript.attributes)) {
        newScript.setAttribute(attr.name, attr.value);
      }
      if (oldScript.textContent) newScript.textContent = oldScript.textContent;
      oldScript.parentNode?.replaceChild(newScript, oldScript);
    }
  }

  private collapseIfEmpty(): void {
    const host = this.hostRef.nativeElement;
    const content = host.querySelector('.magnet-ad-content') as HTMLElement | null;
    if (!content) {
      this.collapse();
      return;
    }
    const hasMeaningfulContent =
      content.children.length > 0 || (content.textContent?.trim().length ?? 0) > 0;
    const hasHeight = content.offsetHeight > 0;

    if (!hasMeaningfulContent || !hasHeight) {
      this.collapse();
    }
  }

  private collapse(): void {
    this.collapsed = true;
    const host = this.hostRef.nativeElement;
    host.innerHTML = '';
  }
}
