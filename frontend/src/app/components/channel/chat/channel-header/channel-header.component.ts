import { Component, EventEmitter, HostListener, Input, OnInit, Output } from '@angular/core';
import { NgIf } from "@angular/common";
import {
  NbButtonModule,
  NbContextMenuModule,
  NbDialogService,
  NbIconModule,
  NbMenuItem,
  NbMenuService,
  NbToastrService,
  NbUserModule
} from "@nebular/theme";
import { InputFormComponent } from "../input-form/input-form.component";
import { filter } from "rxjs";
import { ChannelInfoFormComponent } from '../channel-info-form/channel-info-form.component';
import Viewer from 'viewerjs';
import { Router, RouterLink } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { AuthService, User } from '../../../../services/auth.service';
import { ChatService } from '../../../../services/chat.service';
import { NotificationsService } from '../../../../services/notifications.service';
import { SoundService } from '../../../../services/sound.service';

@Component({
  selector: 'app-channel-header',
  imports: [
    NgIf,
    NbButtonModule,
    NbIconModule,
    NbUserModule,
    NbContextMenuModule,
    RouterLink
  ],
  templateUrl: './channel-header.component.html',
  styleUrl: './channel-header.component.scss'
})
export class ChannelHeaderComponent implements OnInit {

  @Input()
  set userInfo(user: User | undefined) {
    this._userInfo = user;
    this.userMenu = [
      ...((user?.privileges?.['admin'] || user?.privileges?.['moderator']) ? [{
        title: 'ערוך פרטי ערוץ',
        icon: 'edit-2-outline',
      },
      {
        title: 'ניהול ערוץ',
        icon: 'people-outline',
        link: '/admin/dashboard',
      }] : []),
      {
        title: 'התנתק',
        icon: 'log-out',
      }
    ];
  }
  get userInfo() {
    return this._userInfo;
  }
  private _userInfo?: User;

  @Input() adminPanel = false;

  @Output()
  userInfoChange: EventEmitter<User> = new EventEmitter<User>();

  userMenuTag = 'user-menu';
  userMenu: NbMenuItem[] = [];
  isSmallScreen = false;

  constructor(
    public chatService: ChatService,
    private _authService: AuthService,
    private dialogService: NbDialogService,
    private contextMenuService: NbMenuService,
    private toastrService: NbToastrService,
    private router: Router,
    public notificationsService: NotificationsService,
    private titleService: Title,
    public soundService: SoundService,
  ) {
  }

  @HostListener('window:resize')
  onResize() {
    this.updateScreenSize();
  }

  ngOnInit() {
    this.chatService.updateChannelInfo()
      .then(() => this.titleService.setTitle(this.chatService.channelInfo?.name || 'TheChannel'));

    this.contextMenuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.userMenuTag))
      .subscribe(value => {
        switch (value.item.icon) {
          case 'log-out':
            this.logout();
            break;
          case 'edit-2-outline':
            this.openChannelEditerDialog();
            break;
        }
      });

    this.updateScreenSize();
  }

  async logout() {
    if (await this._authService.logout()) {
      this.userInfo = undefined;
      this.userInfoChange.emit(undefined);
      try {
        await this._authService.loadUserInfo();
      } catch (err: any) {
        if (err.status === 401) {
          this.router.navigate(['/login']);
        }
      }

      const path = this.router.url;
      if (path !== '/') {
        this.router.navigate(['/']);
      }

    } else {
      this.toastrService.danger("", "שגיאה בהתנתקות");
    }
  }

  openMessageFormDialog() {
    this.dialogService.open(InputFormComponent, { closeOnBackdropClick: false })
  }

  openChannelEditerDialog() {
    this.dialogService.open(ChannelInfoFormComponent, { closeOnBackdropClick: true, context: { channel: this.chatService.channelInfo } });
  }

  private v!: Viewer;

  viewLargeImage(event: MouseEvent) {
    const target = event.target as HTMLImageElement;
    if (target.tagName === 'IMG') {
      if (!this.v) {
        this.v = new Viewer(target, {
          toolbar: false,
          transition: true,
          navbar: false,
          title: false
        });
      }
      this.v.show();
    }
  }

  updateScreenSize() {
    this.isSmallScreen = window.innerWidth < 768;
  }

  async toggleSound() {
    if (this.soundService.isEnabled()) {
      this.soundService.disableSound();
      this.toastrService.success("", "צלילי התראה הושתקו");
    } else {
      this.soundService.enableSound();

      if (!this.soundService.isInitialized()) {
        await this.soundService.initializeAudioContext();
      }

      this.toastrService.success("", "צלילי התראה הופעלו");
      this.soundService.playNotificationSound();
    }
  }
}
