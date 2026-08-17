import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToggleModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelFeatures } from '../../../services/super-admin.service';

interface FeatureConfig {
  key: keyof ChannelFeatures;
  label: string;
  adminOnly?: boolean;
}

@Component({
  selector: 'app-channel-features',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbToggleModule,
  ],
  templateUrl: './channel-features.component.html',
})
export class ChannelFeaturesComponent implements OnInit {
  @Input() slug!: string;

  features: ChannelFeatures = {
    reactions: false,
    fileUploads: false,
    reports: false,
    ads: false,
    notifications: false,
    requireAuth: false,
    requireAuthFiles: false,
    countViews: false,
    scheduledMessages: false,
    webhook: false,
    magnetLockedByAdmin: false,
    adsLockedByAdmin: false,
  };

  saving = false;

  featureConfigs: FeatureConfig[] = [
    { key: 'reactions', label: 'תגובות' },
    { key: 'fileUploads', label: 'העלאת קבצים' },
    { key: 'reports', label: 'דיווחים' },
    { key: 'ads', label: 'פרסומות iframe' },
    { key: 'notifications', label: 'התראות' },
    { key: 'requireAuth', label: 'דרוש כניסה לצפייה' },
    { key: 'requireAuthFiles', label: 'דרוש כניסה לקבצים' },
    { key: 'countViews', label: 'ספירת צפיות' },
    { key: 'scheduledMessages', label: 'הודעות מתוזמנות' },
    { key: 'webhook', label: 'Webhook' },
    { key: 'magnetLockedByAdmin', label: 'מגנט נעול ע"י מנהל-על', adminOnly: true },
    { key: 'adsLockedByAdmin', label: 'פרסומות iframe נעולות ע"י מנהל-על', adminOnly: true },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadFeatures();
  }

  loadFeatures() {
    this.superAdminService.getChannel(this.slug)
      .then(channel => {
        this.features = { ...channel.features };
      })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת תכונות הערוץ'));
  }

  save() {
    this.saving = true;
    this.superAdminService.updateChannelFeatures(this.slug, this.features)
      .then(() => this.toastr.success('', 'התכונות נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת התכונות'))
      .finally(() => this.saving = false);
  }
}
