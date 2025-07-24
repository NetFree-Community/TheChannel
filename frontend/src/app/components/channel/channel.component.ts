import { Component, OnInit } from '@angular/core';
import { ChatComponent } from "./chat/chat.component";
import { AdvertisingComponent } from "./advertising/advertising.component";
import { CommonModule } from '@angular/common';
import { Ad, AdsService } from '../../services/ads.service';
import {
  NbButtonModule,
  NbIconModule,
  NbLayoutModule,
  NbListModule,
  NbMenuItem,
  NbMenuModule,
  NbSidebarModule,
  NbSidebarService
} from "@nebular/theme";
import { InputFormComponent } from "./chat/input-form/input-form.component";
import { AuthService, User } from "../../services/auth.service";
import { ChannelHeaderComponent } from "./channel-header/channel-header.component";

@Component({
  selector: 'app-channel',
  imports: [
    ChatComponent,
    AdvertisingComponent,
    CommonModule,
    NbLayoutModule,
    InputFormComponent,
    ChannelHeaderComponent,
    NbButtonModule,
    NbIconModule,
    NbMenuModule,
    NbSidebarModule,
    NbListModule,
  ],
  templateUrl: './channel.component.html',
  styleUrl: './channel.component.scss'
})
export class ChannelComponent implements OnInit {

  constructor(
    private adsService: AdsService,
    private _authService: AuthService,
    public sidebarService: NbSidebarService,
  ) { }

  ads: Ad = { src: '', width: 0 };
  userInfo?: User;

  navigationMenu: NbMenuItem[] = [
    {
      title: 'מעבר לערוץ',
      icon: 'arrow-back-outline',
      link: '/',
    },
    {
      title: 'הגדרות ערוץ',
      icon: 'settings-2-outline',
      link: '/admin/settings',
    },
    {
      title: 'ניהול הרשאות',
      icon: 'shield-outline',
      link: '/admin/permissions',
    },
    {
      title: "אימוג'ים",
      icon: 'smiling-face-outline',
      link: '/admin/emojis',
    },
  ]

  ngOnInit(): void {
    this.adsService.getAds().then(ad => {
      this.ads = ad;
    });
    this._authService.loadUserInfo().then(res => this.userInfo = res);
  }
}
