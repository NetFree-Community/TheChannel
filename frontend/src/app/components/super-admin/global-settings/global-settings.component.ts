import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbIconModule, NbInputModule,
  NbToastrService, NbToggleModule, NbAccordionModule,
} from '@nebular/theme';
import { SuperAdminService, Setting } from '../../../services/super-admin.service';

// FCM JSON key map for autofill
const FCM_JSON_KEY_MAP: Record<string, string> = {
  type: 'fcm_json_type',
  project_id: 'fcm_json_project_id',
  private_key_id: 'fcm_json_private_key_id',
  private_key: 'fcm_json_private_key',
  client_email: 'fcm_json_client_email',
  client_id: 'fcm_json_client_id',
  auth_uri: 'fcm_json_auth_uri',
  token_uri: 'fcm_json_token_uri',
  auth_provider_x509_cert_url: 'fcm_json_auth_provider_x509_cert_url',
  client_x509_cert_url: 'fcm_json_client_x509_cert_url',
  universe_domain: 'fcm_json_universe_domain',
};

@Component({
  selector: 'app-global-settings',
  standalone: true,
  imports: [CommonModule, FormsModule, NbCardModule, NbButtonModule, NbInputModule, NbIconModule, NbToggleModule, NbAccordionModule],
  templateUrl: './global-settings.component.html',
})
export class GlobalSettingsComponent implements OnInit {
  values: Record<string, any> = {};
  saving = false;
  loading = true;
  fcmJsonPaste = '';
  fcmJsonError = '';

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.superAdminService.getGlobalSettings()
      .then(settings => this.loadFromSettings(settings || []))
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת הגדרות גלובליות'))
      .finally(() => this.loading = false);
  }

  private loadFromSettings(settings: Setting[]): void {
    // Initialize defaults
    this.values = {
      custom_title: '',
      analytics_head: '',
      on_notification: false,
      project_domain: '',
      vapid: '',
      fcm_api_key: '', fcm_auth_domain: '', fcm_project_id: '',
      fcm_storage_bucket: '', fcm_messaging_sender_id: '', fcm_app_id: '', fcm_measurement_id: '',
      fcm_json_type: '', fcm_json_project_id: '', fcm_json_private_key_id: '',
      fcm_json_private_key: '', fcm_json_client_email: '', fcm_json_client_id: '',
      fcm_json_auth_uri: '', fcm_json_token_uri: '',
      fcm_json_auth_provider_x509_cert_url: '', fcm_json_client_x509_cert_url: '',
      fcm_json_universe_domain: '',
    };
    for (const s of settings) {
      if (s.key === 'on_notification') {
        this.values[s.key] = this.toBool(s.value);
      } else if (s.key in this.values) {
        this.values[s.key] = s.value ?? '';
      }
    }
  }

  private toBool(v: any): boolean {
    if (typeof v === 'boolean') return v;
    if (typeof v === 'string') {
      const s = v.toLowerCase().trim();
      return s === '1' || s === 'true' || s === 'yes' || s === 'on';
    }
    return false;
  }

  private buildSettingsArray(): Setting[] {
    const out: Setting[] = [];
    if (this.values['on_notification'] === true) {
      out.push({ key: 'on_notification', value: '1' as any });
    }
    const textKeys = [
      'custom_title', 'analytics_head',
      'project_domain', 'vapid',
      'fcm_api_key', 'fcm_auth_domain', 'fcm_project_id',
      'fcm_storage_bucket', 'fcm_messaging_sender_id', 'fcm_app_id', 'fcm_measurement_id',
      'fcm_json_type', 'fcm_json_project_id', 'fcm_json_private_key_id',
      'fcm_json_private_key', 'fcm_json_client_email', 'fcm_json_client_id',
      'fcm_json_auth_uri', 'fcm_json_token_uri',
      'fcm_json_auth_provider_x509_cert_url', 'fcm_json_client_x509_cert_url',
      'fcm_json_universe_domain',
    ];
    for (const k of textKeys) {
      const v = this.values[k];
      if (v && v !== '') out.push({ key: k, value: v });
    }
    return out;
  }

  applyFcmJsonPaste(): void {
    this.fcmJsonError = '';
    if (!this.fcmJsonPaste.trim()) return;
    let parsed: any;
    try { parsed = JSON.parse(this.fcmJsonPaste); } catch {
      this.fcmJsonError = 'JSON לא תקין'; return;
    }
    let count = 0;
    for (const [jsonKey, settingKey] of Object.entries(FCM_JSON_KEY_MAP)) {
      if (parsed[jsonKey] !== undefined) { this.values[settingKey] = String(parsed[jsonKey]); count++; }
    }
    if (count === 0) { this.fcmJsonError = 'לא נמצאו שדות מוכרים ב-JSON'; return; }
    this.fcmJsonPaste = '';
    this.toastr.success('', `${count} שדות מולאו אוטומטית`);
  }

  save(): void {
    this.saving = true;
    this.superAdminService.setGlobalSettings(this.buildSettingsArray())
      .then(() => this.toastr.success('', 'ההגדרות נשמרו בהצלחה'))
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת ההגדרות'))
      .finally(() => this.saving = false);
  }
}
