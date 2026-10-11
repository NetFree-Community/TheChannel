import { Component, ElementRef, EventEmitter, HostListener, Input, OnDestroy, OnInit, Output, QueryList, ViewChild, ViewChildren } from '@angular/core';
import { Subscription } from 'rxjs';

import {
  NbButtonModule,
  NbContextMenuDirective,
  NbContextMenuModule,
  NbDialogService,
  NbIconModule,
  NbMenuItem,
  NbMenuService,
  NbPopoverDirective,
  NbPopoverModule,
  NbPosition,
  NbToastrService,
  NbTooltipModule,
  NbUserModule
} from "@nebular/theme";
import { filter } from "rxjs";
import type Viewer from 'viewerjs';
import { Router } from '@angular/router';
import { Title } from '@angular/platform-browser';
import { AuthService } from '../../../services/auth.service';
import { ChatService } from '../../../services/chat.service';
import { NotificationsService } from '../../../services/notifications.service';
import { SlugService } from '../../../services/slug.service';
import { MyChannelsService } from '../../../services/my-channels.service';
import { ShareService } from '../../../services/share.service';
import { SupportBoxComponent } from '../../support/support-box.component';
import { User } from '../../../models/user.model';
import { MyChannel } from '../../../models/my-channel.model';

/** What a menu entry does; carried in NbMenuItem.data so the icon can change freely. */
type MenuAction =
  | 'logout' | 'manage' | 'super-admin' | 'support' | 'create' | 'hub' | 'switch'
  | 'copy' | 'whatsapp' | 'share';

interface MenuData {
  action: MenuAction;
  slug?: string;
}

/** The switcher lists at most this many channels; the rest live on the hub page. */
const MAX_SWITCHER_CHANNELS = 8;

/** The two context menus of the header, by the tag their directive carries. */
type HeaderMenu = 'user-menu' | 'share-menu';

/** The open context menu's items, in the overlay container (outside this component's DOM). */
const MENU_ITEMS = '.cdk-overlay-container nb-context-menu .menu-item > a';

@Component({
  selector: 'app-channel-header',
  imports: [
    NbButtonModule,
    NbIconModule,
    NbUserModule,
    NbContextMenuModule,
    NbTooltipModule,
    NbPopoverModule,
  ],
  templateUrl: './channel-header.component.html',
  styleUrl: './channel-header.component.scss'
})
export class ChannelHeaderComponent implements OnInit, OnDestroy {
  private menuSub?: Subscription;

  @ViewChildren(NbContextMenuDirective) private contextMenus?: QueryList<NbContextMenuDirective>;
  @ViewChild('aboutPop') private aboutPop?: NbPopoverDirective;

  /**
   * Which context menu is open, for aria-expanded and the keyboard handling.
   * Nebular's context menu has no show-state output, so this is set when a
   * trigger is clicked and re-read from the DOM whenever it matters: the menu
   * also closes on its own (outside click, item click) without telling anyone.
   */
  openMenu: HeaderMenu | null = null;
  /** The about card (description + participants) is open. */
  aboutShown = false;

  @Input()
  set userInfo(user: User | undefined) {
    this._userInfo = user;
    // The switcher needs the user's channels; one cached request per user.
    if (user) this.loadMyChannels();
  }

  get userInfo() {
    return this._userInfo;
  }

  private _userInfo?: User;

  /** The signed-in user's channels, for the switcher. Empty until loaded or when the request failed. */
  private myChannels: MyChannel[] = [];
  private myChannelsFor?: User;

  // Derived lazily rather than in the input setter: the user object is cached by
  // AuthService, so switching channels re-emits the very same reference and the
  // setter never runs again — the menu would keep the previous channel's answer.
  // Rebuilt only when the entries actually change, so the context menu directive
  // is not handed a fresh array on every change detection pass.
  get userMenu(): NbMenuItem[] {
    const user = this._userInfo;
    const slug = this._slugService.slug;
    const role = user?.channelRoles?.[slug];
    // Only the role on the channel being viewed counts; a super_admin is granted
    // everything by the backend even with an empty channelRoles map.
    const isSuperAdmin = user?.globalRole === 'super_admin';
    // Every section of the manage page is moderator level or above, so a writer
    // would open it onto an empty sidebar.
    const canManageChannel = !!user && (isSuperAdmin || role === 'owner' || role === 'moderator');
    const key = `${canManageChannel}|${isSuperAdmin}|${slug}|${this.myChannels.map(c => c.slug + ':' + c.name).join(',')}`;

    if (this._menuKey !== key) {
      this._menuKey = key;
      const channels = this.myChannels.slice(0, MAX_SWITCHER_CHANNELS);
      const data = (action: MenuAction, extra: Partial<MenuData> = {}): MenuData => ({ action, ...extra });
      this._userMenu = [
        ...(channels.length ? [
          { title: 'הערוצים שלי', group: true, icon: 'grid-outline' } as NbMenuItem,
          ...channels.map(c => ({
            title: c.name?.trim() || c.slug,
            ariaRole: 'menuitem',
            icon: c.slug === slug ? 'checkmark-circle-2-outline' : 'radio-outline',
            // A router link: Nebular navigates and closes the menu itself.
            link: `/channel/${c.slug}`,
            pathMatch: 'full' as const,
            selected: c.slug === slug,
            data: data('switch', { slug: c.slug }),
          } as NbMenuItem)),
          { title: 'כל הערוצים…', ariaRole: 'menuitem', icon: 'list-outline', link: '/channel', pathMatch: 'full' as const, data: data('hub') } as NbMenuItem,
        ] : []),
        ...(canManageChannel ? [{
          title: 'ניהול הערוץ',
          ariaRole: 'menuitem',
          icon: 'settings-2-outline',
          data: data('manage'),
        }] : []),
        {
          title: 'פתיחת ערוץ חדש',
          ariaRole: 'menuitem',
          icon: 'plus-outline',
          data: data('create'),
        },
        ...(isSuperAdmin ? [{
          title: 'פאנל מנהל-על',
          ariaRole: 'menuitem',
          icon: 'shield-outline',
          data: data('super-admin'),
        }] : []),
        // For every signed-in user, not only those who can open the manage
        // page: the support box otherwise lives on the landing page (which
        // redirects anyone signed in) and in a manage section (moderator and
        // above), so a writer or a plain reader had no way to reach it at all.
        {
          title: 'פנייה לתמיכה',
          ariaRole: 'menuitem',
          icon: 'question-mark-circle-outline',
          data: data('support'),
        },
        {
          title: 'התנתק',
          ariaRole: 'menuitem',
          icon: 'log-out-outline',
          data: data('logout'),
        }
      ];
    }

    return this._userMenu;
  }

  private _userMenu: NbMenuItem[] = [];
  private _menuKey?: string;

  /** "Share the channel" menu: copy, WhatsApp, and the OS share sheet where it exists. */
  readonly shareMenu: NbMenuItem[];
  protected readonly NbPosition = NbPosition;

  @Output()
  userInfoChange: EventEmitter<User> = new EventEmitter<User>();

  userMenuTag = 'user-menu';
  shareMenuTag = 'share-menu';
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
    private myChannelsService: MyChannelsService,
    private shareService: ShareService,
    private hostRef: ElementRef<HTMLElement>,
  ) {
    this.shareMenu = [
      { title: 'העתקת הקישור', ariaRole: 'menuitem', icon: 'copy-outline', data: { action: 'copy' } as MenuData },
      { title: 'שיתוף בוואטסאפ', ariaRole: 'menuitem', icon: 'message-circle-outline', data: { action: 'whatsapp' } as MenuData },
      ...(shareService.canShare
        ? [{ title: 'שיתוף…', ariaRole: 'menuitem', icon: 'share-outline', data: { action: 'share' } as MenuData }]
        : []),
    ];
  }

  @HostListener('window:resize')
  onResize() {
    this.updateScreenSize();
  }

  ngOnInit() {
    this.chatService.updateChannelInfo()
      .then(() => this.titleService.setTitle(this.chatService.channelInfo?.name || 'הערוץ'));

    // One subscription for both menus, keyed on the item's data.action — the
    // icon used to be the key, which broke the moment an icon was redesigned.
    this.menuSub = this.contextMenuService.onItemClick()
      .pipe(filter(({ tag }) => tag === this.userMenuTag || tag === this.shareMenuTag))
      .subscribe(({ item }) => this.onMenuAction(item.data as MenuData | undefined));

    this.updateScreenSize();
  }

  ngOnDestroy() {
    this.menuSub?.unsubscribe();
    // ViewerJS appends its container to document.body — destroy it with the header.
    this.v?.destroy();
  }

  private onMenuAction(data: MenuData | undefined) {
    switch (data?.action) {
      case 'logout':
        this.logout();
        break;
      case 'manage':
        this.router.navigate(['/channel', this._slugService.slug, 'manage']);
        break;
      case 'super-admin':
        this.router.navigate(['/super-admin']);
        break;
      case 'support':
        this.openSupport();
        break;
      case 'create':
        // The hub page owns the create flow (and the "you already own 5"
        // message); the query parameter is a hint it may use to open it at once.
        this.router.navigate(['/channel'], { queryParams: { new: '1' } });
        break;
      case 'copy':
        this.shareService.copy(this.channelUrl).then(ok => {
          if (ok) this.toastrService.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
        });
        break;
      case 'whatsapp':
        window.open(this.shareService.whatsappUrl(this.inviteText), '_blank', 'noopener,noreferrer');
        break;
      case 'share':
        this.shareService.share({ title: this.channelName, text: this.inviteText, url: this.channelUrl })
          .then(outcome => {
            if (outcome === 'unsupported') {
              // No share sheet after all (or it failed): fall back to the clipboard.
              return this.shareService.copy(this.channelUrl).then(ok => {
                if (ok) this.toastrService.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
              });
            }
            return undefined;
          });
        break;
      // 'switch' and 'hub' carry a router link; Nebular navigates on its own.
    }
  }

  // ---- Keyboard support for the overlays -----------------------------------
  // Nebular's context menu opens on click and closes on an outside click, and
  // that is all: Escape does nothing, focus stays on the trigger, and the
  // items — anchors without href at the very end of the document — are out of
  // Tab's reach. The message ⋮ menu is a popover with its own template, so it
  // handles its keys itself; these two menus render Nebular's own component
  // in the overlay container, outside this component's DOM, hence the
  // document-level listener and the DOM queries.

  /** A trigger was clicked (mouse, or Enter/Space turned into a click). */
  onMenuTrigger(menu: HeaderMenu) {
    // The directive attaches the overlay in the same click event; its items
    // exist after the next change detection pass.
    setTimeout(() => {
      const items = this.menuItems();
      if (!items.length) {
        this.openMenu = null;
        return;
      }
      this.openMenu = menu;
      document.querySelector('.cdk-overlay-container nb-context-menu ul.menu-items')?.setAttribute('role', 'menu');
      items.forEach(a => a.setAttribute('tabindex', '0'));
      items[0].focus();
    }, 60);
  }

  @HostListener('document:click')
  onDocumentClick() {
    // An outside click or an item click closed the menu behind our back.
    if (this.openMenu) setTimeout(() => this.syncMenuState());
  }

  @HostListener('document:keydown', ['$event'])
  onDocumentKeydown(event: KeyboardEvent) {
    if (this.aboutShown && (event.key === 'Escape' || event.key === 'Tab')) {
      // No control inside the card: Escape and Tab both leave it. Tab is left
      // to the browser, which then moves on from the trigger as usual.
      if (event.key === 'Escape') event.preventDefault();
      this.aboutPop?.hide();
      this.aboutTrigger()?.focus();
      return;
    }
    if (!this.openMenu) return;
    const items = this.menuItems();
    if (!items.length) {
      this.openMenu = null;
      return;
    }
    const at = items.indexOf(document.activeElement as HTMLElement);
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        event.preventDefault();
        const step = event.key === 'ArrowDown' ? 1 : -1;
        items[at < 0 ? (step > 0 ? 0 : items.length - 1) : (at + step + items.length) % items.length].focus();
        break;
      }
      case 'Home':
      case 'End':
        event.preventDefault();
        items[event.key === 'Home' ? 0 : items.length - 1].focus();
        break;
      case 'Enter':
      case ' ':
        // Plain items are anchors without href, which Enter does not activate;
        // the router-link items it would, so the click is sent once, by hand.
        if (at >= 0) {
          event.preventDefault();
          items[at].click();
        }
        break;
      case 'Escape':
        event.preventDefault();
        this.closeMenu(true);
        break;
      case 'Tab':
        // Tab leaves the menu; close it rather than leave an open overlay
        // behind. Focus goes back to the trigger first, so the browser carries
        // on from there instead of from the end of the document.
        this.closeMenu(at >= 0);
        break;
    }
  }

  private closeMenu(refocus: boolean) {
    const menu = this.openMenu;
    this.openMenu = null;
    if (!menu) return;
    this.contextMenus?.find(d => d.tag === menu)?.hide();
    if (refocus) this.menuTrigger(menu)?.focus();
  }

  /** The DOM is the truth: the menu closes on its own without any event. */
  private syncMenuState() {
    if (this.openMenu && !this.menuItems().length) this.openMenu = null;
  }

  private menuItems(): HTMLElement[] {
    return Array.from(document.querySelectorAll<HTMLElement>(MENU_ITEMS));
  }

  private menuTrigger(menu: HeaderMenu): HTMLElement | null {
    return this.hostRef.nativeElement.querySelector<HTMLElement>(menu === 'user-menu' ? 'nb-user' : '.ch__share');
  }

  private aboutTrigger(): HTMLElement | null {
    return this.hostRef.nativeElement.querySelector<HTMLElement>('.ch__sub');
  }

  /** The about card opened or closed (Nebular reports both). */
  onAboutState(shown: boolean) {
    this.aboutShown = shown;
    // Reading focus moves into the card so a screen reader announces it and
    // Escape works from there; it comes back to the trigger on close.
    if (shown) setTimeout(() => document.querySelector<HTMLElement>('.cdk-overlay-container .ch-about')?.focus());
  }

  /**
   * The user's channels for the switcher. The service caches per user, so this
   * is one request per session; a failure just leaves the group out of the
   * menu — every other entry still works.
   */
  loadMyChannels() {
    const user = this._userInfo;
    if (!user || this.myChannelsFor === user) return;
    this.myChannelsFor = user;
    this.myChannelsService.list()
      .then(list => {
        if (this._userInfo !== user) return;
        this.myChannels = list;
      })
      .catch(() => {
        // Allow a retry on the next open of the menu.
        if (this.myChannelsFor === user) this.myChannelsFor = undefined;
        this.myChannels = [];
      });
  }

  get channelName(): string {
    return this.chatService.channelInfo?.name?.trim() || '';
  }

  get channelUrl(): string {
    return this.shareService.channelUrl(this._slugService.slug);
  }

  private get inviteText(): string {
    return this.shareService.inviteText(this.channelName, this.channelUrl);
  }

  /** First letter of the channel name, for the avatar fallback. */
  get channelInitial(): string {
    return this.channelName.charAt(0) || '?';
  }

  /**
   * The guard and the landing page record where the visitor was before
   * sending them to Google; this button did not, so a reader who signed in
   * from /channel/foo came back to /channel (the "my channel" page, or the
   * onboarding form) with no way back to the channel they were reading. Worse,
   * a stale returnUrl from an earlier guard redirect sent them somewhere else
   * entirely. Record the current URL first, the same way the guard does.
   */
  async login() {
    try {
      localStorage.setItem('returnUrl', this.router.url);
    } catch {
      // Storage unavailable — the login page falls back to /channel.
    }
    try {
      await this._authService.loginWithGoogle();
    } catch {
      // GET /auth/google failed; without this the click did nothing at all.
      this.toastrService.danger("", "ההתחברות אינה זמינה כרגע, נסו שוב מאוחר יותר");
    }
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

  private v?: Viewer;

  async viewLargeImage(event: MouseEvent) {
    // The click lands on the button wrapping the picture (keyboard users too),
    // so the picture is looked up rather than taken from the event target.
    const target = (event.currentTarget as HTMLElement | null)?.querySelector('img') as HTMLImageElement | null;
    if (target) {
      if (!this.v) {
        // Fetched on the first click only: every reader paid for the viewer
        // on page load, and most never open the logo.
        const { default: ViewerCtor } = await import('viewerjs');
        if (this.v) { (this.v as Viewer).update(); (this.v as Viewer).show(); return; }
        this.v = new ViewerCtor(target, {
          toolbar: false,
          transition: true,
          navbar: false,
          title: false
        });
      } else {
        // The logo src may have changed since the viewer cached it.
        this.v.update();
      }
      this.v.show();
    }
  }

  updateScreenSize() {
    this.isSmallScreen = window.innerWidth < 768;
  }

  /** The same box the manage page shows in its 'פנייה לתמיכה' section, as a dialog. */
  openSupport() {
    this.dialogService.open(SupportBoxComponent, {
      closeOnBackdropClick: true,
      context: {
        signedIn: true,
        channelSlug: this._slugService.slug,
        dialogMode: true,
        title: 'פנייה לתמיכה',
        subtitle: 'שאלה, תקלה או בקשה — הפנייה מגיעה להנהלת המערכת, והתשובה תופיע כאן.',
      },
    });
  }

  openContactUs() {
    const url = this.chatService.channelInfo?.contact_us?.trim();
    if (!url) return;
    // contact_us is operator-set free text. window.open on a javascript: value
    // would execute it in the opened window with this origin, so only http(s)
    // and mailto are honoured; anything else is ignored. noopener/noreferrer
    // also stop the opened page from reaching back through window.opener.
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(url);
    if (scheme && !/^(https?|mailto)$/i.test(scheme[1])) return;
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}
