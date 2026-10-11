import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbTagModule,
  NbToggleModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, GlobalAdsConfig } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-ads',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbToggleModule,
    NbTagModule,
    NbAlertModule,
  ],
  templateUrl: './global-ads.component.html',
  styleUrl: './global-ads.component.scss',
})
export class GlobalAdsComponent implements OnInit {
  config: GlobalAdsConfig = {
    src: '',
    width: 300,
    lockAll: false,
    lockedChannels: [],
  };

  loading = true;
  loadFailed = false;
  saving = false;
  newLockedChannel = '';
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  load() {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getAdsConfig()
      .then(cfg => {
        // A server without a stored width answers 0; the field's min is 1,
        // so keep the component's 300 default rather than show 0.
        this.config = {
          ...this.config, ...cfg,
          width: cfg?.width > 0 ? cfg.width : this.config.width || 300,
          lockedChannels: [...(cfg?.lockedChannels || [])],
        };
        this.snapshot = JSON.stringify(this.config);
      })
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'הגדרות הפרסומת לא נטענו');
      })
      .finally(() => this.loading = false);
  }

  get dirty(): boolean {
    return !!this.snapshot && JSON.stringify(this.config) !== this.snapshot;
  }

  /** Same rule the server applies (isFramableURL): http(s) or protocol-relative, or nothing. */
  get srcInvalid(): boolean {
    const v = (this.config.src || '').trim();
    return !!v && !/^(https?:)?\/\/\S+$/i.test(v);
  }

  /** Whether the saved settings reach any channel at all. */
  get appliesNowhere(): boolean {
    return !this.config.lockAll && this.config.lockedChannels.length === 0;
  }

  addLockedChannel() {
    const slug = this.newLockedChannel.trim();
    if (!slug) return;
    if (!this.config.lockedChannels.includes(slug)) {
      this.config.lockedChannels.push(slug);
    }
    this.newLockedChannel = '';
  }

  removeLockedChannel(slug: string) {
    this.config.lockedChannels = this.config.lockedChannels.filter(s => s !== slug);
  }

  save() {
    if (this.srcInvalid) {
      this.toastr.warning('', 'כתובת הפרסומת חייבת להיות כתובת מלאה שמתחילה ב-http:// או https://');
      return;
    }
    this.saving = true;
    this.superAdminService.setAdsConfig(this.config)
      .then(() => {
        this.snapshot = JSON.stringify(this.config);
        this.toastr.success('', 'הגדרות הפרסומת נשמרו');
      })
      .catch((err) => this.toastr.danger('', err?.status === 400
        ? 'כתובת הפרסומת חייבת להיות כתובת מלאה שמתחילה ב-http:// או https://'
        : 'השמירה לא הצליחה, נסו שוב'))
      .finally(() => this.saving = false);
  }
}
