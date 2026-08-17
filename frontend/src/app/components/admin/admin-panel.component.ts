import { Component, OnDestroy, OnInit } from "@angular/core";
import { NbCardModule, NbLayoutModule, NbMenuItem, NbMenuModule, NbMenuService, NbSidebarModule } from "@nebular/theme";
import { filter, Subscription } from "rxjs";
import { SlugService } from "../../services/slug.service";
import { AuthService } from "../../services/auth.service";
import { EmojisComponent } from "./emojis/emojis.component";
import { SettingsComponent } from "./settings/settings.component";
import { PrivilegDashboardComponent } from "./privileg-dashboard/privileg-dashboard.component";
import { ChannelInfoFormComponent } from "../channel/channel-info-form/channel-info-form.component";
import { ReportsComponent } from "./reports/reports.component";
import { StatisticsComponent } from "./statistics/statistics.component";
import { MagnetAdsComponent } from "./magnet-ads/magnet-ads.component";
import { StorageComponent } from "./storage/storage.component";

type MenuAccess = 'any' | 'moderator' | 'owner';

@Component({
  selector: 'admin-dashboard',
  imports: [
    NbLayoutModule,
    NbSidebarModule,
    NbMenuModule,
    NbCardModule,
    EmojisComponent,
    SettingsComponent,
    PrivilegDashboardComponent,
    ChannelInfoFormComponent,
    ReportsComponent,
    StatisticsComponent,
    MagnetAdsComponent,
    StorageComponent
],
  templateUrl: './admin-panel.component.html',
  styleUrls: ['./admin-panel.component.scss']
})
export class AdminPanelComponent implements OnInit, OnDestroy {

  readonly menuTag = "admin-panel";

  readonly info = "info";
  readonly settings = "settings";
  readonly users = "users";
  readonly emojis = "emojis";
  readonly openReports = "open-reports";
  readonly closedReports = "closed-reports";
  readonly allReports = "all-reports";
  readonly statistics = "statistics";
  readonly magnetAds = "magnet-ads";
  readonly storage = "storage";

  selectedMenuItem = this.info;

  navigationMenu: NbMenuItem[] = [];

  // Each tab is backed by an endpoint with its own role gate, so a tab the user
  // cannot use is never rendered — otherwise it just answers 403 with a blank form.
  private readonly menuDefinition: { access: MenuAccess, item: NbMenuItem }[] = [
    {
      // Backed by /admin/edit-channel-info, which the backend gates at moderator —
      // a writer would only ever get a 403 out of this form.
      access: 'moderator',
      item: {
        title: 'פרטי ערוץ',
        icon: 'info-outline',
        selected: true
      }
    },
    {
      access: 'owner',
      item: {
        title: 'הגדרות',
        icon: 'settings-2-outline',
      }
    },
    {
      access: 'owner',
      item: {
        title: 'הרשאות',
        icon: 'shield-outline',
      }
    },
    {
      access: 'moderator',
      item: {
        title: 'סטטיסטיקות',
        icon: 'bar-chart-outline',
      }
    },
    {
      access: 'moderator',
      item: {
        title: "אימוג'ים",
        icon: 'smiling-face-outline',
      }
    },
    {
      access: 'owner',
      item: {
        title: 'שילוב פרסומות ממגנט',
        icon: 'pricetags-outline',
      }
    },
    {
      access: 'owner',
      item: {
        title: 'אחסון',
        icon: 'hard-drive-outline',
      }
    },
    {
      access: 'moderator',
      item: {
        title: 'דיווחים',
        icon: 'alert-triangle-outline',
        children: [
          {
            title: 'פתוחים',
            icon: 'alert-triangle-outline',
          },
          {
            title: 'סגורים',
            icon: 'checkmark-circle-outline',
          },
          {
            title: 'כל הדיווחים',
            icon: 'list-outline',
          }
        ],
      }
    }
  ];

  get channelSlug(): string {
    return this.slugService.slug;
  }

  private menuSub?: Subscription;

  constructor(
    private menuService: NbMenuService,
    private slugService: SlugService,
    private authService: AuthService,
  ) { }

  ngOnInit(): void {
    this.navigationMenu = this.menuDefinition
      .filter(entry => this.canAccess(entry.access))
      .map(entry => entry.item);
    this.handleMenuItemClick()
  }

  ngOnDestroy(): void {
    this.menuSub?.unsubscribe();
  }

  private canAccess(access: MenuAccess): boolean {
    if (access === 'any') return true;
    const user = this.authService.userInfo;
    if (user?.globalRole === 'super_admin') return true;
    const role = user?.channelRoles?.[this.slugService.slug];
    if (access === 'owner') return role === 'owner';
    return role === 'owner' || role === 'moderator';
  }

  handleMenuItemClick() {
    this.menuSub = this.menuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.menuTag))
      .subscribe((event) => {
        this.navigationMenu.forEach((item) => item.selected = false);
        event.item.selected = true;

        switch (event.item.icon) {
          case 'info-outline':
            this.selectedMenuItem = this.info;
            break;
          case 'settings-2-outline':
            this.selectedMenuItem = this.settings;
            break;
          case 'shield-outline':
            this.selectedMenuItem = this.users;
            break;
          case 'smiling-face-outline':
            this.selectedMenuItem = this.emojis;
            break;
          case 'alert-triangle-outline':
            this.selectedMenuItem = this.openReports;
            break;
          case 'checkmark-circle-outline':
            this.selectedMenuItem = this.closedReports;
            break;
          case 'list-outline':
            this.selectedMenuItem = this.allReports;
            break;
          case 'bar-chart-outline':
            this.selectedMenuItem = this.statistics;
            break;
          case 'pricetags-outline':
            this.selectedMenuItem = this.magnetAds;
            break;
          case 'hard-drive-outline':
            this.selectedMenuItem = this.storage;
            break;
        }
      });
  }
}