import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { AdminService } from '../../../services/admin.service';
import { AdsService } from '../../../services/ads.service';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbToastrService,
  NbIconModule,
  NbInputModule,
  NbToggleModule,
  NbAccordionModule,
  NbTooltipModule,
  NbSpinnerModule,
} from "@nebular/theme";

import { FormsModule } from '@angular/forms';
import { Setting } from '../../../models/setting.model';
import { ShareService } from '../../../services/share.service';
import {
  SETTINGS_SCHEMA,
  SettingsCategorySchema,
  SettingFieldSchema,
  getAllKnownKeys,
  PASSTHROUGH_SETTING_KEYS,
  toBool,
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
    NbAlertModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbToggleModule,
    NbAccordionModule,
    NbTooltipModule,
    NbSpinnerModule,
    FormsModule,
  ],
  templateUrl: './settings.component.html',
  styleUrl: './settings.component.scss'
})
export class SettingsComponent implements OnInit {
  /** The settings endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  schema: SettingsCategorySchema[] = SETTINGS_SCHEMA;
  values: Record<string, any> = {};
  regexRules: RegexRule[] = [];
  extraSettings: ExtraSetting[] = [];
  // Settings another tab owns (magnet ads): never shown, re-sent as loaded.
  private passthrough: Setting[] = [];
  // The server copy as last loaded — what "ביטול שינויים" goes back to.
  private lastLoaded: Setting[] = [];
  // Serialised payload at load/save time; anything else means unsaved edits.
  private snapshot = '';

  /** Which password fields are currently shown in clear. */
  revealed: Record<string, boolean> = {};

  setInProgress: boolean = false;
  // Save stays disabled until the server copy arrived: the form starts empty,
  // and settings/set replaces the whole blob (no merge), so a save after a
  // failed load wiped api_secret_key, webhook_*, regex rules and magnet_*.
  loaded = false;
  loading = true;
  loadFailed = false;
  // Super-admin lock on the iframe ad: the ad-iframe-* fields still save, but
  // the public endpoint serves the global src/width and they are ignored.
  adsLocked = false;

  private static readonly AD_IFRAME_KEYS = new Set(['ad-iframe-src', 'ad-iframe-width']);

  constructor(
    private adminService: AdminService,
    private adsService: AdsService,
    private tostService: NbToastrService,
    private shareService: ShareService,
  ) { }

  ngOnInit(): void {
    this.loadSettings();
    // The public ads endpoint is the only place the lock is visible to an
    // owner. A failure here just leaves the hint off; the form still loads.
    this.adsService.getAds()
      .then(ad => this.adsLocked = !!ad?.locked)
      .catch(() => { /* hint only */ });
  }

  get dirty(): boolean {
    return this.loaded && JSON.stringify(this.buildSettingsArray()) !== this.snapshot;
  }

  loadSettings() {
    this.loadFailed = false;
    this.loading = true;
    this.adminService.getSettings()
      .then(settings => {
        this.lastLoaded = settings || [];
        this.loadFromSettings(this.lastLoaded);
        this.loaded = true;
        this.snapshot = JSON.stringify(this.buildSettingsArray());
      })
      .catch((err) => {
        this.loadFailed = true;
        if (err?.status === 403 || err?.status === 401) {
          this.accessDenied.emit();
          return;
        }
        this.tostService.danger('', 'לא הצלחנו לטעון את ההגדרות — נסו שוב');
      })
      .finally(() => this.loading = false);
  }

  /** Back to the server copy, without a round trip. */
  resetChanges() {
    this.loadFromSettings(this.lastLoaded);
    this.revealed = {};
  }

  /** Only the two iframe-ad fields are frozen by the ads lock. */
  isFieldLocked(field: SettingFieldSchema): boolean {
    return this.adsLocked && SettingsComponent.AD_IFRAME_KEYS.has(field.key);
  }

  toggleReveal(key: string) {
    this.revealed[key] = !this.revealed[key];
  }

  /** The owner has to paste the key into the other system; a masked field cannot be read back. */
  async copySecret(key: string) {
    const value = String(this.values[key] ?? '');
    if (!value) return;
    if (await this.shareService.copy(value)) this.tostService.success('', 'הועתק');
  }

  /**
   * Fills a random 32-character key (letters and digits, from the browser's
   * CSPRNG) so the owner never has to invent one — the server rejects
   * anything under 16 characters, and a short key is guessable.
   */
  generateSecret(key: string) {
    const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    this.values[key] = Array.from(bytes, b => alphabet[b % alphabet.length]).join('');
    this.revealed[key] = true;
  }

  private loadFromSettings(settings: Setting[]) {
    const known = getAllKnownKeys();
    const passthroughKeys = new Set<string>(PASSTHROUGH_SETTING_KEYS);
    this.values = {};
    this.regexRules = [];
    this.extraSettings = [];
    this.passthrough = [];

    for (const cat of this.schema) {
      for (const f of cat.fields) {
        if (f.type === 'boolean') this.values[f.key] = false;
        else this.values[f.key] = '';
      }
    }

    for (const s of settings) {
      if (s.key === 'regex-replace') {
        const raw = String(s.value ?? '');
        const idx = regexRuleSeparator(raw);
        if (idx >= 0) {
          this.regexRules.push({
            pattern: unescapeRuleHash(raw.substring(0, idx)),
            replace: raw.substring(idx + 1),
          });
        } else if (raw) {
          this.regexRules.push({ pattern: unescapeRuleHash(raw), replace: '' });
        }
        continue;
      }

      // Checked before `known`: a known key with no form field is a legacy one
      // and is dropped on save, which is exactly what must not happen here.
      if (passthroughKeys.has(s.key)) {
        this.passthrough.push(s);
        continue;
      }

      if (known.has(s.key)) {
        const field = this.findField(s.key);
        if (field?.type === 'boolean') {
          this.values[s.key] = toBool(s.value);
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
      out.push({ key: 'regex-replace', value: `${escapeRuleHash(p)}#${r.replace ?? ''}` as any });
    }

    for (const e of this.extraSettings) {
      const k = (e.key || '').trim();
      if (!k) continue;
      out.push({ key: k, value: e.value ?? '' as any });
    }

    // Re-appended verbatim so a save here never drops the magnet tab's keys.
    out.push(...this.passthrough);

    return out;
  }

  saveSettings() {
    if (!this.loaded) return;
    this.setInProgress = true;
    const payload = this.buildSettingsArray();
    this.adminService.setSettings(payload)
      .then(() => {
        this.tostService.success('', 'ההגדרות נשמרו');
        this.lastLoaded = payload;
        this.snapshot = JSON.stringify(payload);
      })
      .catch((err) => this.tostService.danger('', this.saveErrorText(err)))
      .finally(() => this.setInProgress = false);
  }

  /**
   * validateSettings on the server answers 400 with a plain-text English
   * reason that names the offending key ("webhook_url: must be an absolute
   * http(s) URL", "regex-replace: invalid pattern: …", "max_file_size: must be
   * between 1 and 512 MB", "api_secret_key must be at least 16 characters").
   * Matched on here, never shown as-is.
   */
  private saveErrorText(err: any): string {
    const text = typeof err?.error === 'string' ? err.error : '';
    if (err?.status === 400) {
      if (text.includes('webhook_url')) return 'כתובת העדכון (Webhook) חייבת להתחיל ב-http:// או https://';
      if (text.includes('ad-iframe-src')) return 'כתובת עמוד הפרסומת חייבת להתחיל ב-http:// או https://';
      if (text.includes('regex')) return 'אחד מכללי ההחלפה אינו תקין — בדקו את שדה "מה לחפש"';
      if (text.includes('max_file_size')) return 'גודל הקובץ המרבי חייב להיות בין 1 ל-512 MB';
      if (text.includes('api_secret_key')) return 'המפתח לפרסום מבחוץ חייב להכיל לפחות 16 תווים';
    }
    if (err?.status === 403 || err?.status === 401) return 'אין לכם הרשאה לשנות את ההגדרות של הערוץ';
    return 'שמירת ההגדרות נכשלה — נסו שוב';
  }
}

/**
 * A rule is stored as "<pattern>#<replacement>", and '#' is also an ordinary
 * regex character (a hashtag rule is the obvious case). Splitting at the first
 * bare '#' turned "#(\S+)#**#$1**" into an EMPTY pattern, which the server then
 * applied between every two characters of every message. So a '#' inside the
 * pattern is stored escaped as "\#" — which the regex engine reads as a
 * literal '#' — and the separator is the first '#' that is not escaped.
 * The helpers below mirror splitRegexRule in the backend.
 */
function regexRuleSeparator(raw: string): number {
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === '\\') { i++; continue; }
    if (raw[i] === '#') return i;
  }
  return -1;
}

function escapeRuleHash(pattern: string): string {
  let out = '';
  for (let i = 0; i < pattern.length; i++) {
    if (pattern[i] === '\\' && i + 1 < pattern.length) { out += pattern[i] + pattern[i + 1]; i++; continue; }
    out += pattern[i] === '#' ? '\\#' : pattern[i];
  }
  return out;
}

function unescapeRuleHash(pattern: string): string {
  return pattern.replace(/\\#/g, '#');
}
