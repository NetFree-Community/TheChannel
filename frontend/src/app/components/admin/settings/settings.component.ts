import { Component, OnInit } from '@angular/core';
import { AdminService } from '../../../services/admin.service';
import {
  NbButtonModule,
  NbCardModule,
  NbToastrService,
  NbIconModule,
  NbInputModule,
  NbToggleModule,
  NbAccordionModule,
  NbTooltipModule,
} from "@nebular/theme";

import { FormsModule } from '@angular/forms';
import { CommonModule } from '@angular/common';
import { Setting } from '../../../models/setting.model';
import {
  SETTINGS_SCHEMA,
  SettingsCategorySchema,
  SettingFieldSchema,
  FCM_JSON_KEY_MAP,
  getAllKnownKeys,
} from './settings.schema';

interface RegexRule {
  pattern: string;
  replace: string;
}

interface ExtraSetting {
  key: string;
  value: string;
}

@Component({
  selector: 'app-settings',
  imports: [
    CommonModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbAccordionModule,
    NbTooltipModule,
    FormsModule,
  ],
  templateUrl: './settings.component.html',
  styleUrl: './settings.component.scss'
})
export class SettingsComponent implements OnInit {

  schema: SettingsCategorySchema[] = SETTINGS_SCHEMA;
  values: Record<string, any> = {};
  regexRules: RegexRule[] = [];
  extraSettings: ExtraSetting[] = [];

  setInProgress: boolean = false;
  fcmJsonPaste: string = '';
  fcmJsonError: string = '';

  constructor(
    private adminService: AdminService,
    private tostService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.adminService.getSettings()
      .then(settings => this.loadFromSettings(settings || []))
      .catch(() => this.tostService.danger('', 'אין הרשאה לצפות בהגדרות'));
  }

  private loadFromSettings(settings: Setting[]) {
    const known = getAllKnownKeys();
    this.values = {};
    this.regexRules = [];
    this.extraSettings = [];

    for (const cat of this.schema) {
      for (const f of cat.fields) {
        if (f.type === 'boolean') this.values[f.key] = false;
        else this.values[f.key] = '';
      }
    }

    for (const s of settings) {
      if (s.key === 'regex-replace') {
        const raw = String(s.value ?? '');
        if (raw.includes('#')) {
          const idx = raw.indexOf('#');
          this.regexRules.push({
            pattern: raw.substring(0, idx),
            replace: raw.substring(idx + 1),
          });
        } else if (raw) {
          this.regexRules.push({ pattern: raw, replace: '' });
        }
        continue;
      }

      if (known.has(s.key)) {
        const field = this.findField(s.key);
        if (field?.type === 'boolean') {
          this.values[s.key] = this.toBool(s.value);
        } else {
          this.values[s.key] = s.value === undefined || s.value === null ? '' : String(s.value);
        }
      } else {
        this.extraSettings.push({
          key: s.key,
          value: s.value === undefined || s.value === null ? '' : String(s.value),
        });
      }
    }
  }

  private findField(key: string): SettingFieldSchema | undefined {
    for (const cat of this.schema) {
      const f = cat.fields.find(x => x.key === key);
      if (f) return f;
    }
    return undefined;
  }

  private toBool(v: any): boolean {
    if (typeof v === 'boolean') return v;
    if (typeof v === 'number') return v !== 0;
    if (typeof v === 'string') {
      const s = v.toLowerCase().trim();
      return s === '1' || s === 'true' || s === 'yes' || s === 'on';
    }
    return false;
  }

  isFieldVisible(field: SettingFieldSchema): boolean {
    if (!field.hideWhen) return true;
    return this.values[field.hideWhen.key] !== field.hideWhen.equals;
  }

  addRegexRule() {
    this.regexRules.push({ pattern: '', replace: '' });
  }

  removeRegexRule(i: number) {
    this.regexRules.splice(i, 1);
  }

  addExtraSetting() {
    this.extraSettings.push({ key: '', value: '' });
  }

  removeExtraSetting(i: number) {
    this.extraSettings.splice(i, 1);
  }

  applyFcmJsonPaste() {
    this.fcmJsonError = '';
    if (!this.fcmJsonPaste.trim()) return;
    let parsed: any;
    try {
      parsed = JSON.parse(this.fcmJsonPaste);
    } catch (e) {
      this.fcmJsonError = 'JSON לא תקין';
      return;
    }

    let count = 0;
    for (const [jsonKey, settingKey] of Object.entries(FCM_JSON_KEY_MAP)) {
      if (parsed[jsonKey] !== undefined) {
        this.values[settingKey] = String(parsed[jsonKey]);
        count++;
      }
    }

    if (count === 0) {
      this.fcmJsonError = 'לא נמצאו שדות מוכרים בקובץ ה-JSON';
      return;
    }

    this.fcmJsonPaste = '';
    this.tostService.success('', `${count} שדות מולאו אוטומטית מה-JSON`);
  }

  private buildSettingsArray(): Setting[] {
    const out: Setting[] = [];

    for (const cat of this.schema) {
      for (const f of cat.fields) {
        const v = this.values[f.key];
        if (f.type === 'boolean') {
          if (v === true) out.push({ key: f.key, value: '1' as any });
        } else if (f.type === 'number') {
          if (v !== '' && v !== null && v !== undefined) {
            out.push({ key: f.key, value: String(v) as any });
          }
        } else {
          if (v !== '' && v !== null && v !== undefined) {
            out.push({ key: f.key, value: String(v) as any });
          }
        }
      }
    }

    for (const r of this.regexRules) {
      const p = (r.pattern || '').trim();
      if (!p) continue;
      out.push({ key: 'regex-replace', value: `${p}#${r.replace ?? ''}` as any });
    }

    for (const e of this.extraSettings) {
      const k = (e.key || '').trim();
      if (!k) continue;
      out.push({ key: k, value: e.value ?? '' as any });
    }

    return out;
  }

  saveSettings() {
    this.setInProgress = true;
    const payload = this.buildSettingsArray();
    this.adminService.setSettings(payload)
      .then(() => this.tostService.success('', 'השינויים נשמרו בהצלחה!'))
      .catch(() => this.tostService.danger('', 'שגיאה בשמירת השינויים'))
      .finally(() => this.setInProgress = false);
  }
}
