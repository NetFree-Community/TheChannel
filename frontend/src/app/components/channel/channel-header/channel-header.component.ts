import { Component, EventEmitter, HostListener, Input, OnDestroy, OnInit, Output } from '@angular/core';
import { Subscription } from 'rxjs';

import {
  NbButtonModule,
  NbContextMenuModule, NbDialogService,
  NbIconModule,
  NbMenuItem,
  NbMenuService,
  NbToastrService,
  NbUserModule
} from "@nebular/theme";
import { filter } from "rxjs";
import Viewer from 'viewerjs';
import { Router } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { AuthService } from '../../../services/auth.service';
import { ChatService } from '../../../services/chat.service';
import { NotificationsService } from '../../../services/notifications.service';
import { SlugService } from '../../../services/slug.service';
import { AdminPanelComponent } from "../../admin/admin-panel.component";
import { User } from '../../../models/user.model';

@Component({
  selector: 'app-channel-header',
  imports: [
    NbButtonModule,
    NbIconModule,
    NbUserModule,
    NbContextMenuModule
],
  templateUrl: './channel-header.component.html',
  styleUrl: './channel-header.component.scss'
})
export class ChannelHeaderComponent implements OnInit, OnDestroy {
  private menuSub?: Subscription;

  @Input()
  set userInfo(user: User | undefined) {
    this._userInfo = user;
  }

  get userInfo() {
    return this._userInfo;
  }

  private _userInfo?: User;

  // Derived lazily rather than in the input setter: the user object is cached by
  // AuthService, so switching channels re-emits the very same reference and the
  // setter never runs again — the menu would keep the previous channel's answer.
  // Rebuilt only when the entries actually change, so the context menu directive
  // is not handed a fresh array on every change detection pass.
  get userMenu(): NbMenuItem[] {
    const user = this._userInfo;
    const role = user?.channelRoles?.[this._slugService.slug];
    // Only the role on the channel being viewed counts; a super_admin is granted
    // everything by the backend even with an empty channelRoles map.
    const isSuperAdmin = user?.globalRole === 'super_admin';
    // Every tab of the panel is moderator level or above, so a writer would open
    // it onto an empty sidebar.
    const canManageChannel = !!user && (isSuperAdmin || role === 'owner' || role === 'moderator');

    if (!this._menuState || this._menuState.manage !== canManageChannel || this._menuState.superAdmin !== isSuperAdmin) {
      this._menuState = { manage: canManageChannel, superAdmin: isSuperAdmin };
      this._userMenu = [
        ...(canManageChannel ? [{
          title: 'ניהול ערוץ',
          icon: 'people-outline',
        }] : []),
        ...(isSuperAdmin ? [{
          title: 'פאנל מנהל-על',
          icon: 'shield-outline',
        }] : []),
        {
          title: 'התנתק',
          icon: 'log-out',
        }
      ];
    }

    return this._userMenu;
  }

  private _userMenu: NbMenuItem[] = [];
  private _menuState?: { manage: boolean, superAdmin: boolean };

  @Output()
  userInfoChange: EventEmitter<User> = new EventEmitter<User>();

  userMenuTag = 'user-menu';
  isSmallScreen = false;

  constructor(
    public chatService: ChatService,
    public _authService: AuthService,
    private contextMenuService: NbMenuService,
    private toastrService: NbToastrService,
    private router: Router,
    public notificationsService: NotificationsService,
    private titleService: Title,
    private dialogService: NbDialogService,
    private _slugService: SlugService,
  ) {
  }

  @HostListener('window:resize')
  onResize() {
    this.updateScreenSize();
  }

  ngOnInit() {
    this.chatService.updateChannelInfo()
      .then(() => this.titleService.setTitle(this.chatService.channelInfo?.name || 'TheChannel'));

    this.menuSub = this.contextMenuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.userMenuTag))
      .subscribe(value => {
        switch (value.item.icon) {
          case 'log-out':
            this.logout();
            break;
          case 'people-outline':
            this.dialogService.open(AdminPanelComponent, { closeOnBackdropClick: true });
            break;
          case 'shield-outline':
            this.router.navigate(['/super-admin']);
            break;
        }
      });

    this.updateScreenSize();
  }

  ngOnDestroy() {
    this.menuSub?.unsubscribe();
  }

  async logout() {
    if (await this._authService.logout()) {
      this.userInfo = undefined;
      this.userInfoChange.emit(undefined);
      try {
        await this._authService.loadUserInfo();
        // Still logged in somehow — go to root and let AuthGuard decide
        this.router.navigate(['/']);
      } catch (err: any) {
        if (err.status === 401) {
          this.router.navigate(['/login']);
        }
      }
    } else {
      this.toastrService.danger("", "שגיאה בהתנתקות");
    }
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

  openContactUs() {
    window.open(this.chatService.channelInfo?.contact_us, '_blank');
  }
}
