import { Component, OnInit } from '@angular/core';
import {
  NbLayoutModule,
  NbSidebarModule,
  NbMenuModule,
  NbMenuItem,
  NbIconModule,
  NbSidebarService,
  NbButtonModule,
  NbUserModule
} from "@nebular/theme";
import { RouterOutlet } from "@angular/router";
import { AuthService, User } from '../services/auth.service';
import { CommonModule } from '@angular/common';
import { ChannelHeaderComponent } from '../components/channel/chat/channel-header/channel-header.component';

@Component({
  selector: 'app-main',
  imports: [
    NbLayoutModule,
    RouterOutlet,
    NbSidebarModule,
    NbMenuModule,
    CommonModule,
    ChannelHeaderComponent,
    NbIconModule,
    NbButtonModule,
    NbUserModule,
  ],
  templateUrl: './main.component.html',
  styleUrl: './main.component.scss'
})
export class MainComponent implements OnInit {

  userInfo!: User;
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
      title: 'משתמשים',
      icon: 'people-outline',
      link: '/admin/users',
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

  constructor(
    private _authService: AuthService,
    public sidebarService: NbSidebarService,
  ) { }

  ngOnInit(): void {
    this._authService.loadUserInfo().then(res => this.userInfo = res);
  }
}
