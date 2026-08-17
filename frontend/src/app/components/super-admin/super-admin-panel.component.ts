import { Component, OnDestroy, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { filter, Subscription } from 'rxjs';
import {
  NbButtonModule,
  NbIconModule,
  NbLayoutModule,
  NbMenuItem,
  NbMenuModule,
  NbMenuService,
  NbSidebarModule,
  NbToastrService,
} from '@nebular/theme';
import { Router } from '@angular/router';
import { AuthService } from '../../services/auth.service';
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

type ViewName = 'channels' | 'channel-features' | 'channel-users' | 'channel-storage' | 'ads' | 'magnet' | 'users' | 'settings' | 'statistics' | 'global-storage' | 'requests';

@Component({
  selector: 'app-super-admin-panel',
  standalone: true,
  imports: [
    CommonModule,
    NbLayoutModule,
    NbSidebarModule,
    NbMenuModule,
    NbButtonModule,
    NbIconModule,
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
  ],
  templateUrl: './super-admin-panel.component.html',
  styleUrl: './super-admin-panel.component.scss',
})
export class SuperAdminPanelComponent implements OnInit, OnDestroy {
  selectedView: ViewName = 'channels';
  selectedChannelSlug = '';
  private menuSub?: Subscription;

  readonly MENU_TAG = 'super-admin-menu';

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

  navigationMenu: NbMenuItem[] = [
    { title: 'בקשות לערוצים', icon: 'inbox-outline', selected: false },
    { title: 'ערוצים', icon: 'list-outline', selected: true },
    { title: 'פרסומות iframe', icon: 'film-outline' },
    { title: 'פרסומות מגנט', icon: 'pricetags-outline' },
    { title: 'אחסון גלובלי', icon: 'hard-drive-outline' },
    { title: 'משתמשים', icon: 'people-outline' },
    { title: 'הגדרות גלובליות', icon: 'settings-2-outline' },
    { title: 'סטטיסטיקות', icon: 'bar-chart-outline' },
  ];

  constructor(
    private menuService: NbMenuService,
    private authService: AuthService,
    private router: Router,
    private toastr: NbToastrService,
  ) {}

  async logout() {
    if (await this.authService.logout()) {
      this.router.navigate(['/login']);
    } else {
      this.toastr.danger('', 'שגיאה בהתנתקות');
    }
  }

  ngOnDestroy(): void {
    this.menuSub?.unsubscribe();
  }

  ngOnInit(): void {
    this.menuSub = this.menuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.MENU_TAG))
      .subscribe(event => {
        this.navigationMenu.forEach(item => (item.selected = false));
        event.item.selected = true;

        switch (event.item.icon) {
          case 'inbox-outline':
            this.selectedView = this.VIEW_REQUESTS;
            this.selectedChannelSlug = '';
            break;
          case 'list-outline':
            this.selectedView = this.VIEW_CHANNELS;
            this.selectedChannelSlug = '';
            break;
          case 'film-outline':
            this.selectedView = this.VIEW_ADS;
            break;
          case 'pricetags-outline':
            this.selectedView = this.VIEW_MAGNET;
            break;
          case 'people-outline':
            this.selectedView = this.VIEW_USERS;
            break;
          case 'settings-2-outline':
            this.selectedView = this.VIEW_SETTINGS;
            break;
          case 'hard-drive-outline':
            this.selectedView = this.VIEW_GLOBAL_STORAGE;
            this.selectedChannelSlug = '';
            break;
          case 'bar-chart-outline':
            this.selectedView = this.VIEW_STATISTICS;
            break;
        }
      });
  }

  onEditFeatures(slug: string) {
    this.selectedChannelSlug = slug;
    this.selectedView = this.VIEW_CHANNEL_FEATURES;
  }

  onManageUsers(slug: string) {
    this.selectedChannelSlug = slug;
    this.selectedView = this.VIEW_CHANNEL_USERS;
  }

  onManageStorage(slug: string) {
    this.selectedChannelSlug = slug;
    this.selectedView = this.VIEW_CHANNEL_STORAGE;
  }

  backToChannels() {
    this.selectedView = this.VIEW_CHANNELS;
    this.selectedChannelSlug = '';
  }
}
