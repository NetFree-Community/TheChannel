import { Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { Title } from '@angular/platform-browser';
import {
  NbAccordionModule,
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbLayoutModule,
  NbSelectModule,
  NbSpinnerModule,
  NbTooltipModule,
} from '@nebular/theme';
import { Subscription } from 'rxjs';
import { SlugService } from '../../services/slug.service';
import { ChatService } from '../../services/chat.service';
import { AuthService } from '../../services/auth.service';
import { NotificationsService } from '../../services/notifications.service';
import { MyChannelsService } from '../../services/my-channels.service';
import { MyChannel, ROLE_LABELS, canManage } from '../../models/my-channel.model';
import { ChannelInfoFormComponent } from '../channel/channel-info-form/channel-info-form.component';
import { SettingsComponent } from '../admin/settings/settings.component';
import { PrivilegDashboardComponent } from '../admin/privileg-dashboard/privileg-dashboard.component';
import { StatisticsComponent } from '../admin/statistics/statistics.component';
import { EmojisComponent } from '../admin/emojis/emojis.component';
import { MagnetAdsComponent } from '../admin/magnet-ads/magnet-ads.component';
import { StorageComponent } from '../admin/storage/storage.component';
import { ReportsComponent } from '../admin/reports/reports.component';
import { GuideComponent } from '../admin/guide/guide.component';
import { SupportBoxComponent } from '../support/support-box.component';

type Access = 'moderator' | 'owner';

export interface ManageSection {
  /** URL segment: /channel/:slug/manage/:id */
  id: string;
  title: string;
  icon: string;
  access: Access;
  /** One sentence shown under the title, in the user's words. */
  description: string;
  /** A few words under the menu item. */
  nav: string;
  /** The optional "מה זה?" note — only where the one-liner is not enough. */
  help?: string;
}

type LoadError = '' | 'notfound' | 'forbidden' | 'network';

/**
 * The channel's management screen as a page (/channel/:slug/manage/:tab):
 * deep-linkable, back-button friendly and full width on a phone, instead of
 * the dialog the header used to open over the feed. Sections are keyed by id
 * and routed, so the navigation's selected state follows the URL, and a
 * channel switcher in the header moves between the user's channels without
 * leaving the section they are in.
 */
@Component({
  selector: 'app-manage-channel',
  standalone: true,
  imports: [
    RouterLink,
    NbLayoutModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbSelectModule,
    NbSpinnerModule,
    NbTooltipModule,
    NbAccordionModule,
    ChannelInfoFormComponent,
    SettingsComponent,
    PrivilegDashboardComponent,
    StatisticsComponent,
    EmojisComponent,
    MagnetAdsComponent,
    StorageComponent,
    ReportsComponent,
    GuideComponent,
    SupportBoxComponent,
  ],
  templateUrl: './manage-channel.component.html',
  styleUrl: './manage-channel.component.scss',
})
export class ManageChannelComponent implements OnInit, OnDestroy {
  slug = '';
  tab = 'info';
  sections: ManageSection[] = [];

  loading = true;
  loadError: LoadError = '';
  /** Set when the open tab's endpoint answered 403: the role changed under us. */
  accessDenied = false;

  switcherChannels: MyChannel[] = [];
  /** The current channel is in the switcher only because a super admin is here, not by role. */
  switcherSynthetic = false;
  readonly roleLabels = ROLE_LABELS;

  readonly reportFilters = [
    { id: 'reports', title: 'פתוחים' },
    { id: 'reports-closed', title: 'טופלו' },
    { id: 'reports-all', title: 'כל הדיווחים' },
  ];

  readonly allSections: ManageSection[] = [
    {
      id: 'info', title: 'פרטי הערוץ', icon: 'info-outline', access: 'moderator',
      nav: 'שם, תיאור ולוגו',
      description: 'שם, תיאור, לוגו וקישור ליצירת קשר — מה שהקוראים רואים בראש הערוץ.',
      help: 'השינויים כאן נשמרים רק בלחיצה על "שמירה". כתובת הערוץ (הקישור שמשתפים) נקבעה בפתיחת הערוץ ואינה משתנה מכאן.',
    },
    {
      id: 'settings', title: 'הגדרות', icon: 'settings-2-outline', access: 'owner',
      nav: 'קבצים, התראות, חיבורים',
      description: 'גודל קבצים, התראות לנייד, פרסומת במסגרת וחיבור למערכות חיצוניות.',
      help: 'כל השדות במסך נשמרים יחד בלחיצה על "שמירת שינויים". יכולת שהנהלת המערכת כיבתה לערוץ (למשל העלאת קבצים) לא מופיעה כאן, ואין דרך להדליק אותה מהמסך הזה.',
    },
    {
      id: 'users', title: 'צוות והרשאות', icon: 'people-outline', access: 'owner',
      nav: 'מי כותב ומי מנהל',
      description: 'מי כותב, מי מנהל ומי בעלים — ומוסיפים אנשים לפי כתובת המייל שלהם בגוגל.',
      help: 'הוספה היא לפי כתובת מייל: האדם צריך להתחבר לאתר עם חשבון גוגל של אותה כתובת, ורק אז ההרשאה נכנסת לתוקף. השינויים נשמרים בלחיצה על "שמירה".',
    },
    {
      id: 'statistics', title: 'סטטיסטיקות', icon: 'bar-chart-outline', access: 'moderator',
      nav: 'קוראים וחיבורים',
      description: 'כמה קוראים נכנסו לערוץ, כמה מחוברים עכשיו, ושיא החיבורים לאורך זמן.',
    },
    {
      id: 'emojis', title: 'תגובות אימוג\'י', icon: 'smiling-face-outline', access: 'moderator',
      nav: 'התגובות המותרות',
      description: 'אילו אימוג\'ים הקוראים יכולים להגיב בהם על הודעות בערוץ.',
    },
    {
      id: 'magnet-ads', title: 'פרסומות ממגנט', icon: 'pricetags-outline', access: 'owner',
      nav: 'פרסומות בין ההודעות',
      description: 'שילוב פרסומות של מגנט ADS בין ההודעות — מופעל רק אם יש לכם חשבון וקוד הטמעה ממגנט.',
    },
    {
      id: 'storage', title: 'אחסון', icon: 'hard-drive-outline', access: 'owner',
      nav: 'מקום לקבצים',
      description: 'כמה מקום הקבצים שהועלו לערוץ תופסים, ומה קורה כשהמקום נגמר.',
    },
    {
      id: 'reports', title: 'דיווחים', icon: 'alert-triangle-outline', access: 'moderator',
      nav: 'הודעות שסומנו',
      description: 'הודעות שקוראים סימנו כבעייתיות — כאן סוגרים את הדיווח או מוחקים את ההודעה.',
    },
    {
      id: 'guide', title: 'מדריך למנהל', icon: 'book-open-outline', access: 'moderator',
      nav: 'צעד אחר צעד',
      description: 'כל מה שאפשר לעשות בערוץ, מסך אחרי מסך.',
    },
    {
      id: 'support', title: 'פנייה לתמיכה', icon: 'email-outline', access: 'moderator',
      nav: 'שאלה או תקלה',
      description: 'שאלה, תקלה או בקשה להנהלת המערכת — התשובה תופיע כאן.',
    },
  ];

  @ViewChild('tabStrip') private tabStrip?: ElementRef<HTMLElement>;

  private paramSub?: Subscription;

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private slugService: SlugService,
    private chatService: ChatService,
    private authService: AuthService,
    private notificationsService: NotificationsService,
    private myChannels: MyChannelsService,
    private titleService: Title,
  ) {}

  /** Follows the channel info, so a rename in the "פרטי הערוץ" tab shows at once. */
  get channelName(): string {
    return this.chatService.channelInfo?.name || this.slug;
  }

  get logoUrl(): string {
    return this.chatService.channelInfo?.logoUrl || '';
  }

  get initial(): string {
    return (this.channelName || '?').trim().charAt(0).toUpperCase();
  }

  get section(): ManageSection | undefined {
    return this.sections.find(s => this.isActive(s));
  }

  get showSwitcher(): boolean {
    return this.switcherChannels.length > 1;
  }

  get isSuperAdmin(): boolean {
    return this.authService.userInfo?.globalRole === 'super_admin';
  }

  isActive(s: ManageSection): boolean {
    return this.tab === s.id || (s.id === 'reports' && this.tab.startsWith('reports'));
  }

  link(id: string): string[] {
    return ['/channel', this.slug, 'manage', id];
  }

  ngOnInit(): void {
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('slug') ?? '';
      const tab = params.get('tab');
      if (slug !== this.slug) {
        this.slug = slug;
        // The admin sub-components read the channel through SlugService and
        // ChatService, exactly as the dialog did.
        this.slugService.slug = slug;
        this.chatService.channelInfo = undefined;
        this.notificationsService.reset();
        this.loadChannel();
        this.buildMenu();
        this.loadSwitcher();
      }
      if (!tab) {
        this.router.navigate(this.link(this.sections[0]?.id ?? 'info'), { replaceUrl: true });
        return;
      }
      if (tab !== this.tab) this.accessDenied = false;
      this.tab = tab;
      this.updateTitle();
      this.revealActiveTab();
    });
  }

  ngOnDestroy(): void {
    this.paramSub?.unsubscribe();
  }

  reload(): void {
    this.loadChannel();
  }

  /** A tab's endpoint said 403: the role changed since sign-in. Re-read it and rebuild the menu. */
  onAccessDenied(): void {
    this.accessDenied = true;
    this.authService.reloadUserInfo()
      .then(() => this.buildMenu())
      .catch(() => { /* the card already says what to do */ });
  }

  switchChannel(slug: string): void {
    if (!slug || slug === this.slug) return;
    const target = this.switcherChannels.find(c => c.slug === slug);
    const current = this.section;
    // A moderator on the other channel cannot open an owner-only section
    // there, so land them on the first tab instead of an access card.
    const keepTab = current && (current.access !== 'owner' || this.isSuperAdmin || target?.role === 'owner');
    this.router.navigate(['/channel', slug, 'manage', keepTab ? this.tab : 'info']);
  }

  private loadChannel(): void {
    this.loading = true;
    this.loadError = '';
    const slug = this.slug;
    this.chatService.updateChannelInfo()
      .then(() => { if (slug === this.slug) this.updateTitle(); })
      .catch(err => {
        if (slug !== this.slug) return;
        const status = err?.status;
        this.loadError = status === 404 ? 'notfound' : (status === 401 || status === 403) ? 'forbidden' : 'network';
      })
      .finally(() => { if (slug === this.slug) this.loading = false; });
  }

  private loadSwitcher(): void {
    this.myChannels.list()
      .then(rows => {
        const list = rows.filter(r => this.isSuperAdmin || canManage(r.role));
        // A super admin can manage a channel they hold no role on; keep the
        // current one selectable so the select never shows an empty label.
        this.switcherSynthetic = !list.some(r => r.slug === this.slug);
        if (this.switcherSynthetic) {
          list.unshift({
            slug: this.slug, name: this.channelName, description: '', logoUrl: this.logoUrl,
            role: 'owner', createdAt: '', disabled: false, participants: 0,
          });
        }
        this.switcherChannels = list;
      })
      .catch(() => { this.switcherChannels = []; });
  }

  private updateTitle(): void {
    const s = this.section;
    this.titleService.setTitle(s ? `${s.title} · ${this.channelName}` : `ניהול · ${this.channelName}`);
  }

  /** Scrolls the phone tab strip so the selected tab is visible after a deep link. */
  private revealActiveTab(): void {
    setTimeout(() => {
      const el = this.tabStrip?.nativeElement.querySelector<HTMLElement>('.is-active');
      if (el && typeof el.scrollIntoView === 'function') {
        el.scrollIntoView({ block: 'nearest', inline: 'center' });
      }
    });
  }

  private canAccess(access: Access): boolean {
    const user = this.authService.userInfo;
    if (user?.globalRole === 'super_admin') return true;
    const role = user?.channelRoles?.[this.slug];
    if (access === 'owner') return role === 'owner';
    return role === 'owner' || role === 'moderator';
  }

  private buildMenu(): void {
    this.sections = this.allSections.filter(s => this.canAccess(s.access));
  }
}
