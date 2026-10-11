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
import { ConfirmService } from '../../../services/confirm.service';

interface FeatureConfig {
  key: keyof ChannelFeatures;
  label: string;
  /** One plain sentence: what the toggle does for readers and writers. */
  hint: string;
}

interface FeatureGroup {
  title: string;
  icon: string;
  items: FeatureConfig[];
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
  styleUrl: './channel-features.component.scss',
})
export class ChannelFeaturesComponent implements OnInit {
  @Input() slug!: string;

  features: ChannelFeatures = this.defaults();
  channelName = '';

  saving = false;
  loading = true;
  // Save stays disabled until the server copy arrived: `features` starts as
  // every flag false, and saving that after a failed load switched off every
  // feature of the tenant in one click.
  loaded = false;
  loadFailed = false;
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  readonly groups: FeatureGroup[] = [
    {
      title: 'לקוראים ולכותבים', icon: 'message-circle-outline',
      items: [
        { key: 'reactions', label: 'תגובות', hint: 'הקוראים יכולים להגיב להודעות באימוג׳י.' },
        { key: 'fileUploads', label: 'העלאת קבצים', hint: 'הכותבים יכולים לצרף תמונות וקבצים להודעות. הקבצים נספרים בנפח האחסון של הערוץ.' },
        { key: 'scheduledMessages', label: 'הודעות מתוזמנות', hint: 'הכותבים יכולים לקבוע מועד פרסום עתידי להודעה.' },
        { key: 'countViews', label: 'ספירת צפיות', hint: 'ליד כל הודעה מוצג מספר הצפיות בה.' },
        { key: 'notifications', label: 'התראות לטלפון', hint: 'הקוראים יכולים להירשם להתראה על כל הודעה חדשה. דורש חיבור התראות בהגדרות המערכת.' },
        { key: 'reports', label: 'דיווחים', hint: 'הקוראים יכולים לדווח על הודעה, והדיווח מגיע למנהלי הערוץ.' },
      ],
    },
    {
      title: 'מי יכול לקרוא', icon: 'lock-outline',
      items: [
        { key: 'requireAuth', label: 'כניסה חובה לצפייה', hint: 'רק מי שנכנס עם חשבון יכול לקרוא את הערוץ. כשכבוי, הערוץ פתוח לכל מי שיש לו את הקישור.' },
        { key: 'requireAuthFiles', label: 'כניסה חובה לקבצים', hint: 'קבצים ותמונות נפתחים רק למי שנכנס עם חשבון, גם אם הערוץ עצמו פתוח.' },
      ],
    },
    {
      title: 'פרסום וחיבורים חיצוניים', icon: 'link-2-outline',
      items: [
        { key: 'ads', label: 'פרסומת בערוץ', hint: 'מנהלי הערוץ יכולים להציג פרסומת בתוך הערוץ. אם הערוץ נעול לפרסומת המערכת, היא זו שמוצגת.' },
        { key: 'webhook', label: 'שליחת הודעות לכתובת חיצונית', hint: 'כל הודעה חדשה נשלחת גם לכתובת אינטרנט שמנהלי הערוץ מגדירים (למשל לאוטומציה).' },
      ],
    },
    // magnetLockedByAdmin / adsLockedByAdmin are managed by the global
    // ads/magnet locked-channel lists — the single source of truth. The values
    // loaded from the channel are passed back through save() untouched and
    // shown below as read-only.
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
    private confirm: ConfirmService,
  ) {}

  ngOnInit(): void {
    this.loadFeatures();
  }

  private defaults(): ChannelFeatures {
    return {
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
      disabled: false,
    };
  }

  loadFeatures() {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getChannel(this.slug)
      .then(channel => {
        // Merge over the defaults: an older backend omits newer flags entirely,
        // and an undefined value would leave the toggle unbound.
        this.features = { ...this.defaults(), ...channel.features };
        this.channelName = channel.name || '';
        this.snapshot = JSON.stringify(this.features);
        this.loaded = true;
      })
      .catch((err) => {
        this.loadFailed = true;
        this.toastr.danger('', err?.status === 404
          ? 'הערוץ לא נמצא — ייתכן שנמחק'
          : 'הגדרות הערוץ לא נטענו');
      })
      .finally(() => this.loading = false);
  }

  get dirty(): boolean {
    return this.loaded && JSON.stringify(this.features) !== this.snapshot;
  }

  /** The kill switch asks first: it takes the channel down for everyone. */
  async onDisabledChange(checked: boolean) {
    const was = this.features.disabled;
    // Mirror the toggle's own state first: the binding must move to `true`
    // now so that flipping it back to `false` after a cancelled dialog is a
    // change Angular propagates to the toggle (false → false is not).
    this.features.disabled = checked;
    if (checked && !was) {
      const ok = await this.confirm.ask({
        title: `להשבית את הערוץ "${this.channelName || this.slug}"?`,
        message: 'ההשבתה נכנסת לתוקף בשמירה: הערוץ ייסגר לכל הקוראים והכותבים, כולל הבעלים, ובמקומו יוצג המסך "הערוץ מושבת". אפשר להפעיל אותו מחדש מכאן.',
        status: 'warning',
        icon: 'slash-outline',
        confirmLabel: 'השבתה',
      });
      if (!ok) this.features.disabled = false;
    }
  }

  save() {
    if (!this.loaded) return;
    this.saving = true;
    this.superAdminService.updateChannelFeatures(this.slug, this.features)
      .then(() => {
        this.snapshot = JSON.stringify(this.features);
        this.toastr.success('', 'הגדרות הערוץ נשמרו');
      })
      .catch((err) => this.toastr.danger('', err?.status === 404
        ? 'הערוץ לא נמצא — ייתכן שנמחק'
        : 'השמירה לא הצליחה, נסו שוב'))
      .finally(() => this.saving = false);
  }
}
