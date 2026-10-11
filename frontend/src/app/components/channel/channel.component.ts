import { AfterViewChecked, ChangeDetectorRef, Component, ElementRef, OnDestroy, OnInit, Renderer2, RendererStyleFlags2, ViewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Title } from '@angular/platform-browser';
import { AdvertisingComponent } from "./advertising/advertising.component";

import { Ad, AdsService } from '../../services/ads.service';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbLayoutModule,
  NbListModule,
  NbMenuModule,
  NbSidebarModule,
  NbDialogService,
  NbSpinnerModule,
  NbToastrService,
} from "@nebular/theme";
import { InputFormComponent } from "./chat/input-form/input-form.component";
import { AuthService } from "../../services/auth.service";
import { ChannelHeaderComponent } from "./channel-header/channel-header.component";
import { ChatComponent } from "./chat/chat.component";
import { User } from '../../models/user.model';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { SlugService } from '../../services/slug.service';
import { AdminService } from '../../services/admin.service';
import { ChatService } from '../../services/chat.service';
import { MagnetAdsService } from '../../services/magnet-ads.service';
import { ChannelStatusService } from '../../services/channel-status.service';
import { NotificationsService } from '../../services/notifications.service';
import { MyChannelsService } from '../../services/my-channels.service';
import { ShareService } from '../../services/share.service';
import { MAX_CHANNELS_PER_ACCOUNT } from '../../services/channel.service';
import { MyChannel, ROLE_LABELS, canManage } from '../../models/my-channel.model';
import { CreateChannelFormComponent } from '../channel-create/create-channel-form.component';
import { PlatformAttributionComponent } from './platform-attribution/platform-attribution.component';
import { SupportBoxComponent } from '../support/support-box.component';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-channel',
  imports: [
    FormsModule,
    RouterLink,
    AdvertisingComponent,
    NbLayoutModule,
    NbCardModule,
    NbInputModule,
    NbSpinnerModule,
    InputFormComponent,
    ChannelHeaderComponent,
    NbButtonModule,
    NbIconModule,
    NbMenuModule,
    NbSidebarModule,
    NbListModule,
    ChatComponent,
    CreateChannelFormComponent,
    PlatformAttributionComponent
  ],
  templateUrl: './channel.component.html',
  // The hub (my channels) has its own sheet so each file stays under the
  // component-style budget; both end up on this component.
  styleUrls: ['./channel.component.scss', './channel-hub.scss']
})
export class ChannelComponent implements OnInit, AfterViewChecked, OnDestroy {

  @ViewChild('inputForm', { static: false })
  set inputForm(element: ElementRef) {
    if (element) {
      setTimeout(() => {
        this.updateInputBottomOffset();
      }, 0);
    }
  }

  constructor(
    private adsService: AdsService,
    private _authService: AuthService,
    private renderer: Renderer2,
    private el: ElementRef,
    private route: ActivatedRoute,
    private router: Router,
    private slugService: SlugService,
    private adminService: AdminService,
    private chatService: ChatService,
    private magnetAds: MagnetAdsService,
    private channelStatus: ChannelStatusService,
    private notificationsService: NotificationsService,
    private toastr: NbToastrService,
    private myChannelsService: MyChannelsService,
    private share: ShareService,
    private titleService: Title,
    private dialogService: NbDialogService,
    private cdr: ChangeDetectorRef,
  ) { }

  ad: Ad = { src: '', width: 0 };
  userInfo?: User;
  slugReady = false;
  noChannel = false;

  // The "my channels" hub: shown to a signed-in user at /channel instead of
  // dropping them straight into a channel, so they can switch between their
  // channels, share the links and reach the manage page of each.
  showChannelsList = false;
  myChannels: MyChannel[] = [];
  channelsLoading = false;
  channelsError = '';
  copiedSlug = '';
  readonly roleLabels = ROLE_LABELS;
  readonly maxChannels = MAX_CHANNELS_PER_ACCOUNT;
  readonly canManage = canManage;
  private copyResetTimer?: ReturnType<typeof setTimeout>;
  // Bumped by every initChannel(); an await that resumes under a newer
  // sequence belongs to a page the user already left and must not touch state.
  private initSeq = 0;

  /** Channels this account owns — the only ones the 5-per-account cap counts. */
  get ownedCount(): number {
    return this.myChannels.filter(c => c.role === 'owner').length;
  }

  get canCreateMore(): boolean {
    return this.ownedCount < this.maxChannels;
  }

  /** The direct, shareable URL of a channel. */
  channelUrl(slug: string): string {
    return this.share.channelUrl(slug);
  }

  whatsappHref(channel: MyChannel): string {
    return this.share.whatsappUrl(this.share.inviteText(channel.name, this.channelUrl(channel.slug)));
  }

  get canShareSheet(): boolean {
    return this.share.canShare;
  }

  async copyChannelUrl(slug: string): Promise<void> {
    // The service shows the "copy blocked" notice itself; the link is still
    // reachable through the WhatsApp button and the channel page.
    const ok = await this.share.copy(this.channelUrl(slug));
    this.copiedSlug = ok ? slug : '';
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
    this.copyResetTimer = setTimeout(() => (this.copiedSlug = ''), 2500);
  }

  async shareChannel(channel: MyChannel): Promise<void> {
    const url = this.channelUrl(channel.slug);
    const outcome = await this.share.share({
      title: channel.name,
      text: this.share.inviteText(channel.name, url),
      url,
    });
    if (outcome === 'unsupported') await this.copyChannelUrl(channel.slug);
  }

  enterOwnedChannel(slug: string): void {
    this.router.navigate(['/channel', slug]);
  }

  manageChannel(slug: string): void {
    this.router.navigate(['/channel', slug, 'manage']);
  }

  /**
   * A stable pastel per channel for the initial avatar, so the same channel
   * always gets the same colour across visits and the list does not look like
   * one grey column.
   */
  avatarTone(slug: string): number {
    let h = 0;
    for (let i = 0; i < slug.length; i++) h = (h * 31 + slug.charCodeAt(i)) >>> 0;
    return h % 6;
  }

  initial(channel: MyChannel): string {
    return (channel.name || channel.slug).trim().charAt(0).toUpperCase() || '?';
  }

  /** Switch this page to the create-a-new-channel flow. */
  openCreateChannel(): void {
    if (!this.canCreateMore) return;
    this.showChannelsList = false;
    this.noChannel = true;
    this.titleService.setTitle('פתיחת ערוץ · הערוץ');
    // The URL says what is on screen, so a reload or a shared link lands on
    // the same view. A query-only navigation does not re-emit paramMap, so
    // initChannel() is not re-run and the list is not refetched.
    this.router.navigate([], { relativeTo: this.route, queryParams: { new: '1' }, replaceUrl: true });
  }

  /** Back from the create form to the list (same route, query param dropped). */
  showMyChannelsList(): void {
    this.noChannel = false;
    this.showChannelsList = true;
    this.titleService.setTitle('הערוצים שלי · הערוץ');
    this.router.navigate([], { relativeTo: this.route, queryParams: {}, replaceUrl: true });
    this.loadMyChannels();
  }

  /** Fetches the hub's list; an empty list sends a new user to onboarding. */
  async loadMyChannels(force = false): Promise<void> {
    const seq = this.initSeq;
    this.channelsLoading = true;
    this.channelsError = '';
    try {
      const rows = await this.myChannelsService.list(force);
      if (seq !== this.initSeq) return;
      this.myChannels = rows;
      if (!rows.length) {
        // No channel yet — the onboarding + create-channel flow.
        this.showChannelsList = false;
        this.noChannel = true;
        this.titleService.setTitle('פתיחת ערוץ · הערוץ');
      }
    } catch {
      if (seq !== this.initSeq) return;
      const roles = this.userInfo?.channelRoles;
      if (!roles || !Object.keys(roles).length) {
        // Nothing to list even if the request had worked: go straight to
        // onboarding instead of showing an error about an empty list.
        this.showChannelsList = false;
        this.noChannel = true;
        this.titleService.setTitle('פתיחת ערוץ · הערוץ');
      } else {
        this.channelsError = 'לא הצלחנו לטעון את רשימת הערוצים. בדקו את החיבור לאינטרנט ונסו שוב.';
      }
    } finally {
      if (seq === this.initSeq) this.channelsLoading = false;
    }
  }

  private paramSub?: Subscription;
  /** Watches the fixed header's height; see ngAfterViewChecked. */
  private chromeObserver?: ResizeObserver;
  private chromeTarget?: Element;

  /**
   * Slug of the channel an operator switched off, raised centrally by
   * channelDisabledInterceptor from the 403 {"error":"channel_disabled"} that
   * every /api/channel/{slug}/* call answers. Null while the channel is fine.
   */
  get channelDisabled(): string | null {
    return this.channelStatus.disabledSlug();
  }

  /** Slug that answered 404 on /info — no such channel. Null otherwise. */
  get channelNotFound(): string | null {
    return this.channelStatus.notFoundSlug();
  }

  readonly onboardingSteps = [
    { icon: 'edit-2-outline', title: 'נותנים שם', text: 'הכתובת של הערוץ נקבעת מהשם, ואנחנו בודקים מיד שהיא פנויה.' },
    { icon: 'flash-outline', title: 'לוחצים "פתיחת הערוץ"', text: 'הערוץ נפתח באותו רגע ונרשם על שמכם. בלי המתנה לאישור.' },
    { icon: 'share-outline', title: 'משתפים את הקישור', text: 'כל מי שיש לו את הקישור רואה את מה שאתם מפרסמים — גם בלי להתחבר.' },
  ];

  // Shown on the hub as a reminder of how the channel is run.
  readonly channelHelp = [
    { icon: 'share-outline', title: 'שיתוף הקישור', text: 'כפתורי השיתוף שליד כל ערוץ כאן, או כפתור השיתוף בראש הערוץ, שולחים את הקישור בוואטסאפ או מעתיקים אותו. כל מי שפותח אותו רואה את הערוץ.' },
    { icon: 'paper-plane-outline', title: 'פרסום הודעה', text: 'נכנסים לערוץ וכותבים בתיבה שבתחתית המסך. ההודעה מגיעה מיד לכל הקוראים.' },
    { icon: 'settings-2-outline', title: 'ניהול הערוץ', text: 'הכפתור "ניהול" כאן, או "ניהול הערוץ" בתפריט שנפתח בלחיצה על השם שלכם בראש הערוץ: שם, לוגו, הגדרות, כותבים ומנהלים, סטטיסטיקות.' },
    { icon: 'swap-outline', title: 'מעבר בין ערוצים', text: 'מהדף הזה, או מאותו תפריט בראש כל ערוץ — הערוצים שלכם מופיעים שם תחת "הערוצים שלי".' },
  ];

  ngAfterViewChecked(): void {
    // The header is created and destroyed as slugReady flips, so its arrival
    // has to be noticed here — but cheaply. This does a querySelector and an
    // identity comparison, never a measurement: measuring here meant a forced
    // synchronous layout on every change-detection pass, and change detection
    // runs on every scroll frame because the feed listens for scroll. The
    // measuring is left to the observer below, which fires only when the header
    // actually changes size.
    const header = this.el.nativeElement.querySelector('nb-layout-header');
    if (header === this.chromeTarget) return;

    this.chromeTarget = header ?? undefined;
    this.chromeObserver?.disconnect();
    this.chromeObserver = undefined;
    if (!header) return;

    // Covers both the channel name loading and wrapping to a second line, and
    // any window resize — the header is full width, so a resize changes it too.
    this.chromeObserver = new ResizeObserver(() => this.updateChromeHeight());
    this.chromeObserver.observe(header);
    this.updateChromeHeight();
  }

  ngOnInit(): void {
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('slug');
      this.initChannel(slug);
    });
  }

  ngOnDestroy(): void {
    this.chromeObserver?.disconnect();
    this.paramSub?.unsubscribe();
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
  }

  private async initChannel(slug: string | null): Promise<void> {
    const seq = ++this.initSeq;
    this.slugReady = false;
    this.noChannel = false;
    this.showChannelsList = false;
    this.channelsError = '';
    // A flag raised for the previous channel must not follow us to the next
    // one — but the guard's own /info probe may already have answered for THIS
    // slug (not found / disabled); wiping that would mount the chat shell for
    // one round trip and request everything again before the card appears.
    if (slug === null || (this.channelStatus.notFoundSlug() !== slug && this.channelStatus.disabledSlug() !== slug)) {
      this.channelStatus.reset();
    }
    // Run change detection now so the @if (slugReady) block actually destroys
    // ChatComponent and the header before we reinitialise with the new slug.
    // A microtask yield alone was not enough: no tick runs in it, so
    // false→true collapsed into one frame and the child was never torn down —
    // switching channels kept the previous feed, its SSE stream and its title.
    this.cdr.detectChanges();
    await Promise.resolve();

    if (!slug) {
      // Every branch below must end in a rendered state. An unguarded await
      // here used to throw for a visitor without a session, leaving
      // slugReady=false and noChannel=false — a template that renders nothing
      // at all, i.e. a blank white page.
      let user: User | undefined;
      try {
        user = (await this._authService.loadUserInfo()) ?? undefined;
      } catch {
        user = undefined;
      }
      if (seq !== this.initSeq) return;

      if (!user) {
        // No session: there is no "my channels" list to show, so send the
        // visitor to the login page instead of rendering an empty shell.
        try {
          localStorage.setItem('returnUrl', '/channel');
        } catch {
          // Storage unavailable — the login page just falls back to /channel.
        }
        this.router.navigate(['/login']);
        return;
      }

      this.userInfo = user;
      // Show the hub rather than dropping the user straight into a channel:
      // it is where they switch channels, find the link to share and reach
      // the manage page. Entering a channel is a click from here. A user
      // with no channel at all is sent on to onboarding by loadMyChannels().
      this.showChannelsList = true;
      // The header sets the title to the channel's name inside a channel; on
      // the way back here it would otherwise keep the last channel's name.
      this.titleService.setTitle('הערוצים שלי · הערוץ');
      await this.loadMyChannels();
      // The header's "פתיחת ערוץ חדש" lands here with ?new=1: open the form
      // straight away instead of showing the list first.
      if (seq === this.initSeq && this.showChannelsList
        && this.route.snapshot.queryParamMap.get('new') === '1') {
        this.openCreateChannel();
      }
      return;
    }

    if (seq !== this.initSeq) return;
    this.slugService.slug = slug;
    // Keep the /info the route guard already fetched for this channel.
    this.chatService.clearCache(slug);
    this.adminService.clearCache();
    this.notificationsService.reset();
    // Drop any in-progress edit/compose state from the previous channel, otherwise
    // the replayed BehaviorSubject value makes the input form post the old message
    // id into the new channel.
    this.adminService.setEditMessage(undefined);
    this.magnetAds.clearCache();
    this.slugReady = true;

    // The guard's /info probe already said "no such channel" / "disabled":
    // the card renders off that flag and the shell never mounts, so nothing
    // of the channel's is fetched for it. The session still is — the card's
    // buttons ("לערוצים שלי", "פנייה לתמיכה", logout) depend on it, and
    // /api/user-info is not a request for the missing channel.
    const flagged = this.channelStatus.notFoundSlug() === slug || this.channelStatus.disabledSlug() === slug;

    if (!flagged) {
      this.adsService.getAds().then(ad => {
        this.ad = ad;
      }).catch(() => {
        // No ad column without settings; the chat shell does not depend on it.
      });
    }
    this._authService.loadUserInfo().then(res => {
      this.userInfo = res;
      // Count this signed-in viewer as a channel participant (fire-and-forget).
      if (!flagged) this._authService.registerChannelVisit(slug);
    }).catch(() => {
      // Anonymous visitor on a public channel — read-only view.
      this.userInfo = undefined;
    });
  }

  /**
   * The channel now exists and the session owns it. `loadUserInfo` memoises the
   * cached User, so a plain re-read would hand back an object without the new
   * owner role and the composer would stay hidden — force the refresh first.
   */
  async onChannelCreated(slug: string): Promise<void> {
    try {
      this.userInfo = await this._authService.reloadUserInfo();
    } catch {
      // The channel exists either way; the channel view refetches user-info.
    }
    this.router.navigate(['/channel', slug]);
  }

  backToMyChannels(): void {
    // Same order as goHome(): leave the view, then clear the flag.
    this.router.navigate(['/channel']).then(() => this.channelStatus.reset());
  }

  // For a visitor on the disabled / not-found card: /channel is guarded and
  // would only bounce them to /login, so they are offered the landing page.
  /** The disabled-channel card: the same support box the header menu opens. */
  openSupport(): void {
    this.dialogService.open(SupportBoxComponent, {
      closeOnBackdropClick: true,
      context: {
        signedIn: true,
        channelSlug: this.channelDisabled || '',
        dialogMode: true,
        title: 'פנייה לתמיכה',
        subtitle: 'שאלה, תקלה או בקשה — הפנייה מגיעה להנהלת המערכת, והתשובה תופיע כאן.',
      },
    });
  }

  goHome(): void {
    // Navigate first: clearing the flag while this view is still mounted let
    // the chat shell appear for a frame and fire its requests for the missing
    // channel on the way out. initChannel() resets a stale flag on arrival.
    this.router.navigate(['/']).then(() => this.channelStatus.reset());
  }

  async logout(): Promise<void> {
    if (await this._authService.logout()) {
      this.router.navigate(['/login']);
    } else {
      this.toastr.danger('', 'שגיאה בהתנתקות');
    }
  }

  // A role on some other channel must not open the composer here — gate on the
  // role the user holds on the channel currently being viewed.
  hasAnyRole(user: User | undefined): boolean {
    if (user?.globalRole === 'super_admin') return true;
    const role = user?.channelRoles?.[this.slugService.slug];
    return role === 'owner' || role === 'moderator' || role === 'writer';
  }

  onInputHeightChanged() {
    this.updateInputBottomOffset();
  }

  updateInputBottomOffset() {
    let inputForm = document.getElementById('inputForm');
    let h = inputForm?.clientHeight;
    this.renderer.setStyle(this.el.nativeElement, '--input-height', `${h}px`, RendererStyleFlags2.DashCase);
  }

  /**
   * Publishes the rendered height of the fixed header as `--chrome-height`, so
   * the attribution strip can pin itself just below it.
   *
   * The height comes from a Nebular theme token, but `nb-theme(header-height)`
   * only resolves inside `nb-install-component()` — used outside it the
   * declaration is silently dropped, which is exactly how the strip ended up
   * pinned with `top: auto`. Measuring the element is both correct and immune
   * to a theme change altering the token.
   *
   * Runs after the view exists, and again on resize because the header wraps to
   * a taller layout on a narrow screen.
   */
  private updateChromeHeight = () => {
    const header = this.el.nativeElement.querySelector('nb-layout-header');
    // Guard the zero: during teardown, or before the header paints, writing 0
    // would pin the strip under the header until the next resize.
    const h = header?.getBoundingClientRect().height;
    if (!h) return;
    this.renderer.setStyle(this.el.nativeElement, '--chrome-height', `${Math.round(h)}px`, RendererStyleFlags2.DashCase);
  };
}
