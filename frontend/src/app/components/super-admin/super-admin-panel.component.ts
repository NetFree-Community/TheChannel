import { Component, ViewEncapsulation } from '@angular/core';
import { CommonModule } from '@angular/common';
import { Router, RouterLink } from '@angular/router';
import { Title } from '@angular/platform-browser';
import {
  NbButtonModule,
  NbIconModule,
  NbLayoutModule,
  NbToastrService,
  NbTooltipModule,
} from '@nebular/theme';
import { AuthService } from '../../services/auth.service';
import { ChannelData } from '../../services/super-admin.service';
import { ChannelsListComponent } from './channels/channels-list.component';
import { ChannelFeaturesComponent } from './channels/channel-features.component';
import { ChannelUsersComponent } from './channels/channel-users.component';
import { GlobalAdsComponent } from './global-ads/global-ads.component';
import { GlobalMagnetComponent } from './global-magnet/global-magnet.component';
import { GlobalUsersComponent } from './global-users/global-users.component';
import { GlobalSettingsComponent } from './global-settings/global-settings.component';
import { SuperAdminStatisticsComponent } from './statistics/super-admin-statistics.component';
import { SuperAdminStorageComponent } from './storage/super-admin-storage.component';
import { GlobalStorageComponent } from './global-storage/global-storage.component';
import { ChannelRequestsComponent } from './channel-requests/channel-requests.component';
import { SupportInboxComponent } from './support/support-inbox.component';

type ViewName = 'channels' | 'channel-features' | 'channel-users' | 'channel-storage' | 'ads' | 'magnet' | 'users' | 'settings' | 'statistics' | 'global-storage' | 'requests' | 'support';

interface SectionInfo {
  id: ViewName;
  title: string;
  icon: string;
  /** One plain sentence: what the screen is for and what happens there. */
  description: string;
}

/** The channel a sub-screen (features / users / storage) is opened for. */
interface SelectedChannel {
  slug: string;
  name: string;
}

/**
 * The platform operator's panel: one shell, a section menu, and one section
 * at a time. The menu is a plain list of buttons rather than nb-menu so the
 * same items can lay out as a sidebar on a desktop and as a scrollable strip
 * on a phone without reaching into Nebular's markup.
 *
 * Encapsulation is off on purpose: the `.sa-*` classes below (fields, hints,
 * chips, empty states, save bars) are the shared vocabulary of every section
 * component, and the panel is the only place they ever render. One copy here
 * beats twelve copies that drift. Nothing outside this lazy chunk uses them.
 */
@Component({
  selector: 'app-super-admin-panel',
  standalone: true,
  imports: [
    CommonModule,
    RouterLink,
    NbLayoutModule,
    NbButtonModule,
    NbIconModule,
    NbTooltipModule,
    ChannelsListComponent,
    ChannelFeaturesComponent,
    ChannelUsersComponent,
    GlobalAdsComponent,
    GlobalMagnetComponent,
    GlobalUsersComponent,
    GlobalSettingsComponent,
    SuperAdminStatisticsComponent,
    SuperAdminStorageComponent,
    GlobalStorageComponent,
    ChannelRequestsComponent,
    SupportInboxComponent,
  ],
  templateUrl: './super-admin-panel.component.html',
  styleUrl: './super-admin-panel.component.scss',
  encapsulation: ViewEncapsulation.None,
})
export class SuperAdminPanelComponent {
  selectedView: ViewName = 'channels';
  selectedChannel: SelectedChannel | null = null;
  loggingOut = false;

  readonly VIEW_CHANNELS: ViewName = 'channels';
  readonly VIEW_CHANNEL_FEATURES: ViewName = 'channel-features';
  readonly VIEW_CHANNEL_USERS: ViewName = 'channel-users';
  readonly VIEW_CHANNEL_STORAGE: ViewName = 'channel-storage';
  readonly VIEW_ADS: ViewName = 'ads';
  readonly VIEW_MAGNET: ViewName = 'magnet';
  readonly VIEW_USERS: ViewName = 'users';
  readonly VIEW_SETTINGS: ViewName = 'settings';
  readonly VIEW_STATISTICS: ViewName = 'statistics';
  readonly VIEW_GLOBAL_STORAGE: ViewName = 'global-storage';
  readonly VIEW_REQUESTS: ViewName = 'requests';
  readonly VIEW_SUPPORT: ViewName = 'support';

  /** The menu, in the order it is shown. */
  readonly sections: SectionInfo[] = [
    {
      id: 'channels', title: 'ערוצים', icon: 'list-outline',
      description: 'כל הערוצים במערכת. מכאן פותחים ערוץ, משנים את התכונות שלו, מנהלים משתמשים ואחסון, משביתים או מוחקים.',
    },
    {
      id: 'requests', title: 'בקשות לערוצים', icon: 'inbox-outline',
      description: 'בקשות לפתיחת ערוץ שהגיעו מהאתר. אישור יוצר את הערוץ וממנה את המבקש לבעלים; דחייה נשמרת עם הסבר.',
    },
    {
      id: 'support', title: 'פניות למערכת', icon: 'email-outline',
      description: 'כל הפניות שנשלחו להנהלת המערכת — ממנהלי ערוצים ומגולשים באתר. התשובה מופיעה לפונה במקום שממנו פנה.',
    },
    {
      id: 'users', title: 'משתמשים', icon: 'people-outline',
      description: 'כל מי שנכנס למערכת או קיבל הרשאה, והתפקיד שלו בכל ערוץ. תפקידים משנים במסך המשתמשים של הערוץ.',
    },
    {
      id: 'ads', title: 'פרסומת במסגרת', icon: 'film-outline',
      description: 'פרסומת אחת של המערכת, במסגרת (iframe) בתוך דף הערוץ, שמוצגת בערוצים הנעולים אליה. ערוץ שאינו נעול מציג את הפרסומת שמנהליו הגדירו.',
    },
    {
      id: 'magnet', title: 'פרסומות מגנט', icon: 'pricetags-outline',
      description: 'הגדרות המגנט של המערכת. הן חלות רק על ערוצים שנעולים אליהן — שאר הערוצים משתמשים בהגדרות שלהם.',
    },
    {
      id: 'global-storage', title: 'אחסון', icon: 'hard-drive-outline',
      description: 'נפח האחסון שכל ערוץ מקבל כברירת מחדל לקבצים ולתמונות. לערוץ מסוים קובעים נפח אחר מרשימת הערוצים.',
    },
    {
      id: 'settings', title: 'הגדרות מערכת', icon: 'settings-2-outline',
      description: 'כותרת האתר, קוד אנליטיקס וחיבור ההתראות לטלפון — הגדרות משותפות לכל הערוצים.',
    },
    {
      id: 'statistics', title: 'סטטיסטיקות', icon: 'bar-chart-outline',
      description: 'נתוני הפרסומות ממגנט, ואיפוס מוני החיבורים של כל הערוצים.',
    },
  ];

  /** Sub-screens of a single channel, reached from the channels list. */
  private readonly channelViews: Record<string, Omit<SectionInfo, 'id'>> = {
    'channel-features': {
      title: 'הגדרות הערוץ', icon: 'settings-2-outline',
      description: 'מה מופעל בערוץ, מי יכול לקרוא בו, והשבתה זמנית. השינויים נכנסים לתוקף מיד לאחר השמירה.',
    },
    'channel-users': {
      title: 'משתמשי הערוץ', icon: 'people-outline',
      description: 'מי מנהל את הערוץ ומי כותב בו. בעלים שולט בכול, מנהל מנהל וכותב, וכותב רק מפרסם הודעות.',
    },
    'channel-storage': {
      title: 'אחסון הערוץ', icon: 'hard-drive-outline',
      description: 'כמה מקום הקבצים של הערוץ תופסים, וכמה מותר לו. 0 משאיר את ברירת המחדל של המערכת.',
    },
  };

  constructor(
    private authService: AuthService,
    private router: Router,
    private toastr: NbToastrService,
    private titleService: Title,
  ) {
    this.titleService.setTitle('ניהול מערכת · הערוץ');
  }

  get isChannelView(): boolean {
    return this.selectedView === this.VIEW_CHANNEL_FEATURES
      || this.selectedView === this.VIEW_CHANNEL_USERS
      || this.selectedView === this.VIEW_CHANNEL_STORAGE;
  }

  /** The intro (title + explanation) of whatever is on screen. */
  get current(): Omit<SectionInfo, 'id'> {
    if (this.isChannelView) return this.channelViews[this.selectedView];
    return this.sections.find(s => s.id === this.selectedView) || this.sections[0];
  }

  /** The menu item to highlight — channel sub-screens belong to "ערוצים". */
  get activeSection(): ViewName {
    return this.isChannelView ? this.VIEW_CHANNELS : this.selectedView;
  }

  select(view: ViewName) {
    this.selectedView = view;
    this.selectedChannel = null;
    // A phone shows the menu strip above the content: bring the new
    // section's title into view instead of leaving the reader mid-page.
    if (typeof window !== 'undefined') window.scrollTo({ top: 0 });
  }

  async logout() {
    this.loggingOut = true;
    try {
      if (await this.authService.logout()) {
        this.router.navigate(['/login']);
      } else {
        this.toastr.danger('', 'ההתנתקות לא הצליחה, נסו שוב');
      }
    } finally {
      this.loggingOut = false;
    }
  }

  onEditFeatures(channel: ChannelData) {
    this.openChannelView(channel, this.VIEW_CHANNEL_FEATURES);
  }

  onManageUsers(channel: ChannelData) {
    this.openChannelView(channel, this.VIEW_CHANNEL_USERS);
  }

  onManageStorage(channel: ChannelData) {
    this.openChannelView(channel, this.VIEW_CHANNEL_STORAGE);
  }

  backToChannels() {
    this.select(this.VIEW_CHANNELS);
  }

  private openChannelView(channel: ChannelData, view: ViewName) {
    this.selectedChannel = { slug: channel.slug, name: channel.name };
    this.selectedView = view;
    if (typeof window !== 'undefined') window.scrollTo({ top: 0 });
  }
}
