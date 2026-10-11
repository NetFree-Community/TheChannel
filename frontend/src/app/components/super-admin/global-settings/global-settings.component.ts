import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule, NbCardModule, NbFormFieldModule, NbIconModule, NbInputModule,
  NbToastrService, NbToggleModule, NbTooltipModule,
} from '@nebular/theme';
import { SuperAdminService, Setting } from '../../../services/super-admin.service';
import { toBool } from '../../admin/settings/settings.schema';

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

/** One text field of the Firebase forms: the key Firebase shows, and a Hebrew hint. */
interface FieldDef {
  key: string;
  label: string;
  hint?: string;
  /** Whole-row field (long URLs). */
  wide?: boolean;
}

@Component({
  selector: 'app-global-settings',
  standalone: true,
  imports: [
    CommonModule, FormsModule, NbCardModule, NbButtonModule, NbInputModule, NbIconModule,
    NbToggleModule, NbFormFieldModule, NbTooltipModule,
  ],
  templateUrl: './global-settings.component.html',
  styleUrl: './global-settings.component.scss',
})
export class GlobalSettingsComponent implements OnInit {
  values: Record<string, any> = {};
  saving = false;
  loading = true;
  loadFailed = false;
  fcmJsonPaste = '';
  fcmJsonError = '';
  /** Secrets are masked until the operator asks to see them. */
  showVapid = false;
  showPrivateKey = false;
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  /** Firebase SDK ("web app") config — what Firebase Console shows under "Your apps". */
  readonly sdkFields: FieldDef[] = [
    { key: 'fcm_api_key', label: 'apiKey' },
    { key: 'fcm_auth_domain', label: 'authDomain' },
    { key: 'fcm_project_id', label: 'projectId' },
    { key: 'fcm_storage_bucket', label: 'storageBucket' },
    { key: 'fcm_messaging_sender_id', label: 'messagingSenderId' },
    { key: 'fcm_app_id', label: 'appId' },
    { key: 'fcm_measurement_id', label: 'measurementId', hint: 'לא חובה.' },
  ];

  /** Service-account JSON fields, except the private key which has its own masked field. */
  readonly serviceAccountFields: FieldDef[] = [
    { key: 'fcm_json_type', label: 'type' },
    { key: 'fcm_json_project_id', label: 'project_id' },
    { key: 'fcm_json_private_key_id', label: 'private_key_id' },
    { key: 'fcm_json_client_email', label: 'client_email' },
    { key: 'fcm_json_client_id', label: 'client_id' },
    { key: 'fcm_json_universe_domain', label: 'universe_domain' },
    { key: 'fcm_json_auth_uri', label: 'auth_uri', wide: true },
    { key: 'fcm_json_token_uri', label: 'token_uri', wide: true },
    { key: 'fcm_json_auth_provider_x509_cert_url', label: 'auth_provider_x509_cert_url', wide: true },
    { key: 'fcm_json_client_x509_cert_url', label: 'client_x509_cert_url', wide: true },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getGlobalSettings()
      .then(settings => {
        this.loadFromSettings(settings || []);
        this.snapshot = JSON.stringify(this.values);
      })
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'הגדרות המערכת לא נטענו');
      })
      .finally(() => this.loading = false);
  }

  get dirty(): boolean {
    return !!this.snapshot && JSON.stringify(this.values) !== this.snapshot;
  }

  /** How many of the required Firebase fields are still empty, for the hint. */
  get missingPushFields(): number {
    const required = [
      'project_domain', 'vapid',
      'fcm_api_key', 'fcm_auth_domain', 'fcm_project_id', 'fcm_storage_bucket',
      'fcm_messaging_sender_id', 'fcm_app_id',
      'fcm_json_project_id', 'fcm_json_private_key', 'fcm_json_client_email',
    ];
    return required.filter(k => !String(this.values[k] ?? '').trim()).length;
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
        this.values[s.key] = toBool(s.value);
      } else if (s.key in this.values) {
        this.values[s.key] = s.value ?? '';
      }
    }
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
      this.fcmJsonError = 'הטקסט שהודבק אינו קובץ JSON תקין. הדביקו את כל תוכן הקובץ, מהסוגר הפותח ועד הסוגר הסוגר.'; return;
    }
    let count = 0;
    for (const [jsonKey, settingKey] of Object.entries(FCM_JSON_KEY_MAP)) {
      if (parsed[jsonKey] !== undefined) { this.values[settingKey] = String(parsed[jsonKey]); count++; }
    }
    if (count === 0) { this.fcmJsonError = 'לא נמצאו בקובץ שדות של חשבון שירות. ודאו שזה הקובץ שהורדתם מ-Firebase.'; return; }
    this.fcmJsonPaste = '';
    this.toastr.success('', `${count} שדות מולאו מהקובץ. זכרו לשמור.`);
  }

  save(): void {
    this.saving = true;
    this.superAdminService.setGlobalSettings(this.buildSettingsArray())
      .then(() => {
        this.snapshot = JSON.stringify(this.values);
        this.toastr.success('', 'הגדרות המערכת נשמרו');
      })
      .catch((err) => this.toastr.danger('', err?.status === 400
        ? 'אחד הערכים אינו תקין, בדקו את השדות ונסו שוב'
        : 'השמירה לא הצליחה, נסו שוב'))
      .finally(() => this.saving = false);
  }
}
