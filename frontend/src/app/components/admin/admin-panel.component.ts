import { Component, OnInit } from "@angular/core";
import { NbCardModule, NbLayoutModule, NbMenuItem, NbMenuModule, NbMenuService, NbSidebarModule } from "@nebular/theme";
import { EmojisComponent } from "./emojis/emojis.component";
import { SettingsComponent } from "./settings/settings.component";
import { UsersComponent } from "./users/users.component";
import { PrivilegDashboardComponent } from "./privileg-dashboard/privileg-dashboard.component";
import { ChannelInfoFormComponent } from "../channel/channel-info-form/channel-info-form.component";

@Component({
  selector: 'admin-dashboard',
  imports: [
    NbLayoutModule,
    NbSidebarModule,
    NbMenuModule,
    NbCardModule,
    EmojisComponent,
    SettingsComponent,
    UsersComponent,
    PrivilegDashboardComponent,
    ChannelInfoFormComponent,
  ],
  templateUrl: './admin-panel.component.html',
  styleUrls: ['./admin-panel.component.scss']
})
export class AdminPanelComponent implements OnInit {

  readonly info = "info";
  readonly settings = "settings";
  readonly users = "users";
  readonly emojis = "emojis";

  selectedMenuItem = this.info;

  navigationMenu: NbMenuItem[] = [
    {
      title: 'פרטי ערוץ',
      icon: 'info-outline',
      selected: true
    },
    {
      title: 'הגדרות',
      icon: 'settings-2-outline',
    },
    {
      title: 'הרשאות',
      icon: 'shield-outline',
    },
    {
      title: "אימוג'ים",
      icon: 'smiling-face-outline',
    },
  ];

  constructor(
    private menuService: NbMenuService
  ) {}

  ngOnInit(): void {
    this.handleMenuItemClick()
  }

  handleMenuItemClick() {
    this.menuService.onItemClick().subscribe((event) => {
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
      }
    });
  }
}