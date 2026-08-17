import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
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
  ],
  templateUrl: './global-ads.component.html',
})
export class GlobalAdsComponent implements OnInit {
  config: GlobalAdsConfig = {
    src: '',
    width: 300,
    lockAll: false,
    lockedChannels: [],
  };

  saving = false;
  newLockedChannel = '';

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.superAdminService.getAdsConfig()
      .then(cfg => this.config = { ...cfg, lockedChannels: [...(cfg.lockedChannels || [])] })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת הגדרות פרסומות'));
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
    this.saving = true;
    this.superAdminService.setAdsConfig(this.config)
      .then(() => this.toastr.success('', 'הגדרות הפרסומות נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת הגדרות הפרסומות'))
      .finally(() => this.saving = false);
  }
}
