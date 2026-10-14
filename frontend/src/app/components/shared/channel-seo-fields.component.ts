import { Component, EventEmitter, Input, Output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { NbIconModule, NbInputModule, NbToggleModule } from '@nebular/theme';

/**
 * A channel's search-engine settings: whether it may appear in Google, and
 * the title and description the result shows, with a preview of that result.
 * Used by the channel's own "פרטי הערוץ" screen and by the super admin's
 * channel screen; each owns the save. The limits and the fallbacks mirror
 * backend/channel_seo.go.
 */
@Component({
  selector: 'app-channel-seo-fields',
  standalone: true,
  imports: [FormsModule, NbIconModule, NbInputModule, NbToggleModule],
  templateUrl: './channel-seo-fields.component.html',
  styleUrl: './channel-seo-fields.component.scss',
})
export class ChannelSeoFieldsComponent {
  /** Shown in search (the inverse of the stored seoHidden). */
  @Input() listed = true;
  @Output() listedChange = new EventEmitter<boolean>();
  @Input() seoTitle = '';
  @Output() seoTitleChange = new EventEmitter<string>();
  @Input() seoDescription = '';
  @Output() seoDescriptionChange = new EventEmitter<string>();

  @Input() slug = '';
  @Input() channelName = '';
  @Input() channelDescription = '';
  /** Private or disabled: never in search, whatever the toggle says. */
  @Input() blockedReason = '';
  /** Distinguishes the inputs' ids when two copies share a page. */
  @Input() idPrefix = 'seo';

  readonly titleMax = 60;
  readonly descriptionMax = 155;

  private readonly siteName =
    document.querySelector('meta[name="application-name"]')?.getAttribute('content') || 'הערוץ';

  get host(): string {
    return location.host;
  }

  get channelUrl(): string {
    return `${location.origin}/channel/${this.slug}`;
  }

  get previewTitle(): string {
    const t = oneLine(this.seoTitle);
    return t || `${oneLine(this.channelName) || this.slug} · ${this.siteName}`;
  }

  get previewDescription(): string {
    const d = oneLine(this.seoDescription) || oneLine(this.channelDescription)
      || `ערוץ העדכונים ${oneLine(this.channelName) || this.slug}: הודעות, תמונות ועדכונים, ישירות בדפדפן ובלי אפליקציה.`;
    return d.length > 160 ? d.slice(0, 159).trimEnd() + '…' : d;
  }

  get inSearch(): boolean {
    return this.listed && !this.blockedReason;
  }
}

function oneLine(s: string | undefined | null): string {
  return (s || '').split(/\s+/).filter(Boolean).join(' ');
}
