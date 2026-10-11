
import { Component, OnInit, NgZone, OnDestroy, HostListener } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  NbButtonModule,
  NbIconModule,
  NbListModule,
  NbToastrService,
  NbTooltipModule
} from "@nebular/theme";
import { MessageComponent } from "./message/message.component";
import { MagnetAdSlotComponent } from "./magnet-ad-slot/magnet-ad-slot.component";
import { firstValueFrom, interval, Subscription } from 'rxjs';
import { ChatMessage, ChatService } from '../../../services/chat.service';
import { AuthService } from '../../../services/auth.service';
import { ActivatedRoute, Router } from '@angular/router';
import { NotificationsService } from '../../../services/notifications.service';
import { User } from '../../../models/user.model';
import { AdminService } from '../../../services/admin.service';
import { MagnetAdsService } from '../../../services/magnet-ads.service';
import { SlugService } from '../../../services/slug.service';
import { ChannelStatusService } from '../../../services/channel-status.service';
import { ShareService } from '../../../services/share.service';
import { calendarDayDiff, formatDayMonthYear, formatWeekday } from '../../../pipes/message-time.pipe';

type LoadMsgOpt = {
  scrollDown?: boolean;
  messageId?: number;
  mark?: boolean;
  resetList?: boolean;
  // Targeted load whose outcome the caller inspects itself: no scroll and no
  // "not found" toast afterwards.
  quiet?: boolean;
}

type ScrollOpt = {
  messageId: number;
  smooth?: boolean;
  mark?: boolean;
}

@Component({
  selector: 'app-chat',
  standalone: true,
  imports: [
    NbIconModule,
    NbButtonModule,
    NbListModule,
    NbTooltipModule,
    MessageComponent,
    MagnetAdSlotComponent
  ],
  templateUrl: './chat.component.html',
  styleUrl: './chat.component.scss'
})
export class ChatComponent implements OnInit, OnDestroy {
  private eventSource!: EventSource;
  private sseEverConnected = false;
  // Id of the last stream entry received, for the manual reconnects below:
  // only the browser's own retry sends Last-Event-ID, a fresh EventSource
  // starts at the tip and silently loses whatever was published in between.
  private lastEventId = '';
  messages: ChatMessage[] = [];
  adSlotsAfter: Set<number> = new Set();
  /**
   * Message id → the day heading shown above it ("היום", "אתמול", or
   * "יום שלישי, 3.3.2026"): the first loaded message of each calendar day.
   * Recomputed with the ad slots whenever the list changes.
   */
  dayLabels: Map<number, string> = new Map();
  scheduledMessages!: ChatMessage[];
  hideScheduledMessages: boolean = false;
  userInfo?: User;
  isLoading: boolean = false;
  isOffline: boolean = false;
  isVisible: boolean = false;   // hidden until initial scroll is resolved
  initialLoadFailed = false;    // true when the first history load errored
  private lastLoadOk = true;    // set by loadMessages; false after a failed load
  offset: number = 0;
  limit: number = 20;
  hasOldMessages: boolean = true;
  hasNewMessages: boolean = false;
  thereNewMessages: boolean = false;
  /** How many messages arrived while the reader was scrolled up; shown on the pill. */
  newMessagesCount: number = 0;
  showScrollToBottom: boolean = false;
  private lastHeartbeat: number = Date.now();
  private subLastHeartbeat?: Subscription;
  private schedulingSub?: Subscription;
  private fragmentSub?: Subscription;
  private fragmentTimer: any;
  lastReadMessageId: number = 0;

  constructor(
    private chatService: ChatService,
    private _authService: AuthService,
    private _adminService: AdminService,
    private toastrService: NbToastrService,
    private notificationService: NotificationsService,
    private magnetAds: MagnetAdsService,
    private slugService: SlugService,
    private channelStatus: ChannelStatusService,
    private zone: NgZone,
    private router: ActivatedRoute,
    private share: ShareService,
    private nav: Router,
  ) { }

  /** Whether the viewer can post here; decides which empty-state text to show. */
  get canWrite(): boolean {
    return this.hasWriteRole();
  }

  /** Moderator level and above: may open the manage page from the empty state. */
  get canManage(): boolean {
    const user = this.userInfo;
    if (!user) return false;
    if (user.globalRole === 'super_admin') return true;
    const role = user.channelRoles?.[this.slugService.slug];
    return role === 'owner' || role === 'moderator';
  }

  /** Empty state, writers: the first thing an owner wants is readers. */
  async copyChannelLink() {
    const ok = await this.share.copy(this.share.channelUrl(this.slugService.slug));
    if (ok) this.toastrService.success('', 'הקישור לערוץ הועתק — אפשר להדביק ולשלוח');
  }

  openChannelInfo() {
    this.nav.navigate(['/channel', this.slugService.slug, 'manage', 'info']);
  }

  // A role on some other channel grants nothing here — the scheduled-messages
  // routes are gated per channel, so only the role on the current slug counts.
  private hasWriteRole(): boolean {
    const user = this.userInfo;
    if (!user) return false;
    if (user.globalRole === 'super_admin') return true;
    const role = user.channelRoles?.[this.slugService.slug];
    return role === 'owner' || role === 'moderator' || role === 'writer';
  }

  @HostListener('window:online')
  onOnline() {
    this.zone.run(() => {
      this.isOffline = false;
      this.toastrService.success('החיבור חודש', '', { duration: 3000 });
      this.initializeMessageListener();
      this.loadMissedMessages();
    });
  }

  @HostListener('window:offline')
  onOffline() {
    this.zone.run(() => { this.isOffline = true; });
  }

  // The window scroll listener lives outside Angular (see ngOnInit): it used
  // to be a HostListener, which scheduled a change-detection pass over every
  // message card for each scroll event while flicking through the feed.
  private scrollRaf = 0;
  private readonly onWindowScroll = () => {
    if (this.scrollRaf) return;
    this.scrollRaf = requestAnimationFrame(() => {
      this.scrollRaf = 0;
      const distanceFromBottom = document.documentElement.scrollHeight - window.innerHeight - window.scrollY;
      const show = distanceFromBottom > 100;
      const reached = distanceFromBottom < 10 && (this.thereNewMessages || this.newMessagesCount > 0);
      if (show === this.showScrollToBottom && !reached) return;
      this.zone.run(() => {
        this.showScrollToBottom = show;
        if (reached) {
          this.thereNewMessages = false;
          this.newMessagesCount = 0;
        }
      });
    });
  };

  /** A tab left open past midnight: "היום" has become "אתמול". */
  @HostListener('document:visibilitychange')
  onVisibilityChange() {
    if (document.visibilityState === 'visible' && this.dayLabelsDay !== this.todayKey()) {
      this.dayLabels = this.computeDayLabels(this.messages);
    }
  }

  @HostListener('document:keydown', ['$event'])
  @HostListener('window:click', ['$event'])
  onUserAction(event: MouseEvent | KeyboardEvent) {
    this.removeMsgMarked();
    const target = event.target as HTMLElement;
    const quoteElement = target.closest('[quote-id]')
    if (quoteElement) {
      const quoteId = quoteElement.getAttribute('quote-id');
      this.scrollToId({ messageId: Number(quoteId), smooth: true, mark: true });
    }
  }

  scrollToId(opt: ScrollOpt, attempt: number = 0) {
    const id = opt.messageId;
    // Number() of a non-numeric quote id is NaN, which used to reach
    // loadMessages as a target no page could ever contain.
    if (!Number.isInteger(id) || id <= 0) {
      this.notifyMessageNotFound();
      return;
    }
    const element = document.getElementById(id.toString());
    if (element) {
      element.scrollIntoView({ behavior: opt.smooth ? 'smooth' : 'instant', block: 'center' });
      this.removeMsgMarked();
      opt.mark && element.classList.add('mark_message');
      return;
    }
    if (this.messages.some(m => m.id === id)) {
      // Loaded but not painted yet (change detection is still pending right
      // after a load resolves). Wait for the DOM instead of fetching: another
      // page would only move the window away from a message already here.
      if (attempt < 10) setTimeout(() => this.scrollToId(opt, attempt + 1), 50);
      return;
    }
    // Outside the loaded window: ONE targeted load, after which loadMessages
    // either scrolls or gives up. It used to call back here after every
    // response, and this branch fetched again, so a target that never arrives
    // (a deleted message a reader cannot see, a stale quote or last-read id)
    // crawled the whole history page by page.
    this.loadMessages({ scrollDown: false, messageId: id, mark: opt.mark });
  }

  private notifyMessageNotFound() {
    this.toastrService.warning('', 'ההודעה לא נמצאה (ייתכן שנמחקה)');
  }

  private removeMsgMarked() {
    document.querySelectorAll('.mark_message').forEach((el) => {
      el.classList.remove('mark_message');
    });
  }

  ngAfterViewInit(): void {
    this.fragmentTimer = setTimeout(() => {
      this.fragmentSub = this.router.fragment.subscribe(fragment => {
        if (fragment) {
          const messageId = Number(fragment);
          if (!Number.isInteger(messageId)) return;
          // After NavigationEnd: nb-layout's restoreScrollTop scrolls to 0 in
          // its own NavigationEnd handler, which undid an earlier
          // scrollIntoView on a same-document #id change.
          setTimeout(() => this.scrollToId({ messageId, mark: true }), 0);
        }
      });
    }, 800);
  }

  ngOnInit() {
    this.schedulingSub = this._adminService.schedulingBusObservable.subscribe(() => {
      this.loadScheduledMessages();
    });

    this.chatService.getEmojisList(true).catch(() => null);

    this.magnetAds.loadSettings()
      .catch(() => null)
      .then(() => this.rebuildItems());

    this.zone.runOutsideAngular(() => window.addEventListener('scroll', this.onWindowScroll, { passive: true }));

    this.initializeMessageListener();
    this.keepAliveSSE();

    this._authService.loadUserInfo().then((res) => {
      this.userInfo = res;
      this.loadScheduledMessagesIfAllowed();
      this.notificationService.init();
    }).catch(() => {
      // Anonymous visitor on a public channel — read-only view.
      this.userInfo = undefined;
    });

    this.loadMessages().then(() => this.revealAfterInitialLoad());
  }

  /**
   * Reveals the feed once the initial history load resolves. If that load
   * failed and left the feed empty, it shows a retry affordance instead of a
   * silent empty channel.
   */
  private async revealAfterInitialLoad(): Promise<void> {
    if (!this.lastLoadOk && !this.messages.length) {
      // Failed load, not an empty channel: surface it so the user can retry.
      this.initialLoadFailed = true;
      this.isVisible = true;
      return;
    }
    this.initialLoadFailed = false;

    // Namespaced per channel — message ids are per-channel sequences, so a
    // shared key would carry channel A's position into channel B.
    const lastReadMsg = Number(localStorage.getItem(`lastReadMessage:${this.slugService.slug}`));
    const lastMsgId = this.messages[0]?.id;
    if (!lastMsgId) { this.isVisible = true; return; }

    // A stored id must be a real message id older than the tip; anything else
    // (NaN from a hand-edited value, 0 from a missing key) is a first visit.
    const wantsLastRead = Number.isInteger(lastReadMsg) && lastReadMsg > 0 && lastReadMsg < lastMsgId;
    let lastReadLoaded = wantsLastRead && this.messages.some(m => m.id === lastReadMsg);
    if (wantsLastRead && !lastReadLoaded) {
      // Either more than a page unread, or a stale id (deleted since, or a
      // reset history). ONE load positioned around the id — the same jump
      // scrollToId uses — tells them apart; the old fallback paged through
      // the entire history looking for it on every visit.
      await this.loadMessages({ scrollDown: false, messageId: lastReadMsg, quiet: true });
      lastReadLoaded = this.messages.some(m => m.id === lastReadMsg);
    }

    if (lastReadLoaded) {
      // Set the indicator BEFORE revealing the list so the line renders
      // at the right position on first paint — no visible jump.
      this.lastReadMessageId = lastReadMsg;
      this.scrollToId({ messageId: lastReadMsg, smooth: false, mark: false });
      // The message is loaded; scrollToId only has to wait for it to paint.
      // Reveal after that, and setLastReadMessage only once the user has
      // actually seen the position — so a refresh still brings them back.
      setTimeout(() => {
        this.isVisible = true;
        this.setLastReadMessage(lastMsgId.toString());
      }, 350);
    } else {
      // Not there: back to the tip (the jump above may have paged away from
      // it, which scrollToBottom reloads) and the stale key is overwritten
      // with the current tip, like a first visit.
      this.lastReadMessageId = 0;
      await this.scrollToBottom(false);
      this.isVisible = true;
      this.setLastReadMessage(lastMsgId.toString());
    }
  }

  async setLastReadMessage(id: string) {
    localStorage.setItem(`lastReadMessage:${this.slugService.slug}`, id);
  }

  private initializeMessageListener() {
    this.eventSource = this.chatService.sseListener(this.lastEventId);

    this.eventSource.onopen = () => {
      if (this.sseEverConnected) {
        // Reconnect after a drop — fetch messages that arrived during the gap
        this.zone.run(() => { this.isOffline = false; });
        this.loadMissedMessages();
        // The scheduled list is only refreshed by the dispatch event; one that
        // fell into the gap left a stale cache, and the writer's next save
        // re-posted the already-published entry from it.
        this.zone.run(() => this.loadScheduledMessagesIfAllowed(true));
      }
      this.sseEverConnected = true;
      this.lastHeartbeat = Date.now();
    };

    this.eventSource.onerror = () => {
      // Browser will auto-retry with Last-Event-ID; we just track offline state
      // via window:online/offline and the keepAlive heartbeat.
    };

    this.eventSource.onmessage = (event) => {
      this.lastHeartbeat = Date.now();
      if (event.lastEventId) this.lastEventId = event.lastEventId;

      const message = JSON.parse(event.data);
      switch (message.type) {
        case 'channel-deleted':
          // Published right before the channel is removed; every request to
          // it answers 404 from here on. Without this the page kept looking
          // live, and the heartbeat watchdog reconnected to a 404 forever.
          this.onChannelGone();
          break;
        case 'channel-disabled':
          // Same as above for the super admin's kill switch: the stream is
          // closed server-side and reconnects answer 403, so show the
          // disabled page instead of reconnecting into it.
          this.onChannelGone(true);
          break;
        case 'new-message':
          if (this.hasNewMessages) break;
          if (this.messages.some(m => m.id === message.message.id)) break; // dedup after reconnect
          this.zone.run(() => {
            this.messages.unshift(message.message);
            this.rebuildItems();
            const authorId = message.message.authorId;
            // An operator's posts carry a shared stand-in id rather than their
            // own, so their own post must not raise the new-messages dot.
            const mine = authorId === this.userInfo?.id
              || (authorId === 'operator' && this.userInfo?.globalRole === 'super_admin');
            // Measured before the new card is painted: at the bottom now means
            // the reader wants to stay there — and the author of a post always
            // does, or their own message sat hidden under the fixed composer
            // with no scroll and no "newer messages" button to reach it.
            const atBottom = this.isAtBottom();
            this.thereNewMessages = !atBottom && !mine;
            this.newMessagesCount = this.thereNewMessages ? this.newMessagesCount + 1 : 0;
            if (atBottom || mine) this.scrollToBottom(false);
            this.setLastReadMessage(message.message.id!.toString());
            if (this.hasWriteRole() && this.scheduledMessages && this.cameFromScheduler(message.message)) {
              this.loadScheduledMessages(true);
            }
          });
          break;
        case 'delete-message':
          if (this.hasWriteRole()) {
            this.zone.run(() => {
              const index = this.messages.findIndex(m => m.id === message.message.id);
              if (index !== -1) {
                this.messages[index].deleted = true;
                this.messages[index].last_edit = message.message.last_edit;
              }
            });
            break;
          };
          this.zone.run(() => {
            this.messages = this.messages.filter(m => m.id !== message.message.id);
            this.rebuildItems();
          });
          break;
        case 'edit-message':
          this.zone.run(() => {
            const index = this.messages.findIndex(m => m.id === message.message.id);
            if (index !== -1) {
              this.messages[index] = message.message;
            } else if (!message.message.deleted) {
              // Not in the list: this is a message that was deleted (readers
              // drop deleted messages, see 'delete-message') and has now been
              // republished. Put it back where it falls in the loaded window
              // — the list is in the server's time order, not id order, so
              // the timestamp is the key. Beyond an end of the window that
              // still has pages to load it is left to those pages; inserting
              // it there would place it next to a gap.
              const edited: ChatMessage = message.message;
              const when = (m: ChatMessage) => new Date(m.timestamp!).getTime();
              const newest = this.messages[0];
              const oldest = this.messages[this.messages.length - 1];
              const beyondNewest = !!newest && when(edited) > when(newest) && this.hasNewMessages;
              const beyondOldest = !!oldest && when(edited) < when(oldest) && this.hasOldMessages;
              if (!beyondNewest && !beyondOldest) {
                const at = this.messages.findIndex(m => when(m) < when(edited));
                this.messages.splice(at === -1 ? this.messages.length : at, 0, edited);
                this.rebuildItems();
              }
            }
          });
          break;
        case 'reaction':
          this.zone.run(() => {
            const index = this.messages.findIndex(m => m.id === message.message.id);
            if (index !== -1) this.messages[index].reactions = message.message.reactions;
          });
          break;
        case 'heartbeat':
          this.lastHeartbeat = Date.now();
          break;
      }
    };
  }

  ngOnDestroy() {
    window.removeEventListener('scroll', this.onWindowScroll);
    cancelAnimationFrame(this.scrollRaf);
    this.chatService.sseClose();
    this.subLastHeartbeat?.unsubscribe();
    this.schedulingSub?.unsubscribe();
    clearTimeout(this.fragmentTimer);
    this.fragmentSub?.unsubscribe();
  }

  /**
   * The channel no longer exists (a channel-deleted event, or a 404 from a
   * message route — the slug middleware answers 404 before any of them runs).
   * Stop the stream and its watchdog so nothing keeps reconnecting, and raise
   * the not-found flag the channel page already renders for a wrong address.
   */
  private onChannelGone(disabled = false) {
    this.subLastHeartbeat?.unsubscribe();
    this.chatService.sseClose();
    this.zone.run(() => {
      const slug = this.slugService.slug;
      if (disabled) this.channelStatus.markDisabled(slug);
      else this.channelStatus.markNotFound(slug);
    });
  }

  // The scheduler used to post under the literal author "Scheduled"; it now
  // posts under the scheduler's own display name, so the author alone no
  // longer tells a dispatched scheduled message from a regular one. A
  // pending entry whose time had come by the new message's timestamp does.
  private cameFromScheduler(incoming: ChatMessage): boolean {
    if (incoming.author === 'Scheduled') return true;
    if (!this.scheduledMessages?.length) return false;
    const at = new Date(incoming.timestamp!).getTime();
    return this.scheduledMessages.some(s => new Date(s.timestamp!).getTime() <= at);
  }

  private rebuildItems() {
    try {
      this.adSlotsAfter = this.magnetAds.computeAdSlots(this.messages);
    } catch (e) {
      console.error('computeAdSlots failed, ads will not be shown:', e);
      this.adSlotsAfter = new Set();
    }
    this.dayLabels = this.computeDayLabels(this.messages);
  }

  /**
   * The list is newest-first and rendered column-reverse, so the message that
   * sits visually above index i is index i+1. A heading goes on a message whose
   * upper neighbour is on another calendar day (or does not exist). The oldest
   * loaded message always gets one; when an older page arrives the heading
   * simply moves up to that page's first message of the day.
   */
  private dayLabelsDay = '';

  private todayKey(): string {
    const d = new Date();
    return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
  }

  private computeDayLabels(messages: ChatMessage[]): Map<number, string> {
    const labels = new Map<number, string>();
    const now = new Date();
    this.dayLabelsDay = this.todayKey();
    const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
    for (let i = 0; i < messages.length; i++) {
      const m = messages[i];
      if (m.id === undefined) continue;
      const d = new Date(m.timestamp as any);
      if (isNaN(d.getTime())) continue;
      const above = messages[i + 1];
      const aboveDate = above ? new Date(above.timestamp as any) : null;
      const sameDay = !!aboveDate && !isNaN(aboveDate.getTime()) && dayKey(aboveDate) === dayKey(d);
      if (sameDay) continue;
      const diff = calendarDayDiff(d, now);
      const label = diff === 0 ? 'היום'
        : diff === -1 ? 'אתמול'
        : `${formatWeekday(d)}, ${formatDayMonthYear(d)}`;
      labels.set(m.id, label);
    }
    return labels;
  }

  async keepAliveSSE() {
    this.subLastHeartbeat?.unsubscribe();
    // Backend sends heartbeat every 25s; if 35s pass without one the SSE is dead.
    this.subLastHeartbeat = interval(10000)
      .subscribe(() => {
        if (Date.now() - this.lastHeartbeat > 35000) {
          this.lastHeartbeat = Date.now();
          this.initializeMessageListener();
          this.loadMissedMessages();
          // The scheduled list is reloaded by the new EventSource's onopen
          // (sseEverConnected is already set), so no second GET here.
        }
      });
  }

  private async loadMissedMessages() {
    if (!this.messages.length) return;
    // The server resumes AFTER the given id's rank in its time index, and the
    // list is kept in that order, so the cursor is index 0 — not the largest
    // id: after a backdated import the largest id can sit deep in the past,
    // and paging up from it re-fetched the loaded window.
    const newestId = this.messages[0].id!;
    try {
      const missed = await firstValueFrom(this.chatService.getMessages(newestId, this.limit, 'asc'));
      if (!missed?.length) return;

      this.zone.run(() => {
        // Deduplicate: SSE may have already delivered some of these via Last-Event-ID.
        const existing = new Set(this.messages.map(m => m.id));
        const fresh = missed.filter(m => !existing.has(m.id));
        if (!fresh.length) return;

        // fresh is in ascending order; reverse so newest is at index 0
        this.messages.unshift(...[...fresh].reverse());
        this.hasNewMessages = missed.length >= this.limit;
        this.rebuildItems();

        // The browser's overflow-anchor CSS property handles viewport
        // preservation automatically when content is added at the visual
        // bottom (flex-column-reverse puts new items there). We only need
        // to act when the user is already at the bottom: scroll them to
        // see the new messages immediately.
        if (this.isAtBottom()) {
          this.thereNewMessages = false;
          this.newMessagesCount = 0;
          this.scrollToBottom(false);
        } else {
          this.thereNewMessages = true;
          this.newMessagesCount += fresh.length;
        }
      });
    } catch (error) {
      if (error instanceof HttpErrorResponse && error.status === 404) this.onChannelGone();
      // Otherwise best-effort; will retry on the next reconnect
    }
  }

  private isAtBottom(): boolean {
    const distanceFromBottom =
      document.documentElement.scrollHeight - window.innerHeight - window.scrollY;
    return distanceFromBottom < 80;
  }

  private getScrollAnchorElement(): Element | null {
    // Pick the topmost fully-visible message as the scroll anchor.
    const items = document.querySelectorAll('nb-list-item');
    for (const el of Array.from(items)) {
      const rect = el.getBoundingClientRect();
      if (rect.top >= 0 && rect.bottom <= window.innerHeight) return el;
    }
    return null;
  }

  async scrollToBottom(smooth: boolean = true) {
    if (this.hasNewMessages) {
      this.hasNewMessages = false;
      await this.loadMessages({ resetList: true });
    }
    setTimeout(() => {
      window.scrollTo({ top: document.body.scrollHeight, behavior: smooth ? 'smooth' : 'instant' });
    }, 200);
    this.thereNewMessages = false;
    this.newMessagesCount = 0;
  }

  // The endpoint answers 403 when the operator switched scheduled messages off,
  // and the flag only arrives with the channel info — which is still in flight on
  // first paint, so wait for it instead of firing a request that cannot succeed.
  private async loadScheduledMessagesIfAllowed(reload: boolean = false) {
    if (!this.hasWriteRole()) return;
    await this.chatService.ensureChannelInfo().catch(() => null);
    if (!this.chatService.scheduledMessagesEnabled) return;
    this.loadScheduledMessages(reload);
  }

  private async loadScheduledMessages(reload: boolean = false) {
    this._adminService.getScheduledMessages(reload)
      .then(messages => {
        this.scheduledMessages = messages;
      }).catch(() => this.toastrService.danger('', "הייתה בעיה בטעינת ההודעות המתוזמנות."));
  }

  async loadMessages(opt: LoadMsgOpt = {}) {
    if (this.isLoading) return;
    // The "nothing further" flags describe the ends of the current window; a
    // targeted load (messageId) positions its own page and may go either way,
    // and a reset starts over from the tip regardless of where the window was
    // (after a jump that landed near the channel's first message, the "no
    // older" flag used to refuse the very reload that scrollToBottom needs).
    if (!opt.messageId && !opt.resetList && (opt.scrollDown ? !this.hasNewMessages : !this.hasOldMessages)) return;

    // Reset before the attempt; the catch flips it. Read after the call to tell
    // an empty feed caused by a failed request from a genuinely empty channel.
    this.lastLoadOk = true;
    let startId: number;
    let resetList: boolean = opt.resetList || false;
    let direction: string = "desc";

    opt.resetList && (this.offset = 0);

    const maxId = this.messages.length ? Math.max(...this.messages.map(m => m.id!)) : 0;
    // Cursor for an asc page: the server resumes after the given id's rank in
    // its time index and the list is kept in that order, so it is the newest
    // loaded message, index 0 — not the largest id, which after a backdated
    // import can be the OLDEST message in the window, so the same page was
    // fetched and unshifted again (the desc cursor below had the same fix).
    // maxId stays for the deep-link arithmetic only, which is id-based.
    const newestId = this.messages.length ? this.messages[0].id! : 0;
    if (opt.scrollDown) {
      direction = "asc";
      startId = newestId;
    } else {
      if (opt.messageId) {
        if (opt.messageId > maxId + this.limit) {
          // Newer than the next page up. An asc page starts AFTER the offset
          // key in the time index, so a start above the target (id + 10, as
          // this used to do) returned a page that could not contain it — and
          // when that id did not exist yet the Lua found no rank and began at
          // the channel's very first message. Ten below puts the target
          // around the tenth row.
          resetList = true;
          this.hasNewMessages = true;
          this.hasOldMessages = true;
          startId = Math.max(0, opt.messageId - 10);
          direction = "asc";
          opt.scrollDown = true;
        } else if (opt.messageId > maxId) {
          startId = newestId;
          direction = "asc";
          opt.scrollDown = true;
        } else {
          if (opt.messageId < this.offset - this.limit) {
            resetList = true;
            this.hasNewMessages = true;
            this.hasOldMessages = true;
            startId = opt.messageId + 10;
          } else {
            startId = this.offset;
          }
        }
      } else {
        startId = this.offset;
      }
    }

    try {
      this.isLoading = true;
      const response = await firstValueFrom(this.chatService.getMessages(startId, this.limit, direction))
      if (response) {
        // Drop ids already in the list (as loadMissedMessages does): an SSE
        // push or a jump can leave the window overlapping the page, and the
        // feed tracks by object identity, so a repeated id rendered twice.
        // The "more pages" flags read the raw page size, not the survivors.
        const existing = new Set(this.messages.map(m => m.id));
        const fresh = resetList ? response : response.filter(m => !existing.has(m.id));
        if (opt.scrollDown) {
          resetList ? this.messages = fresh.reverse() : this.messages.unshift(...fresh.reverse());
          this.hasNewMessages = response.length >= this.limit;
        } else {
          resetList ? this.messages = fresh : this.messages.push(...fresh);
          this.hasOldMessages = response.length >= this.limit;
        }
        // The server pages by position in the time-ordered index, resuming
        // after the given id — and the list is kept in that same order. The
        // cursor is therefore the LAST loaded message, not the smallest id:
        // ids and timestamps diverge once posts are imported with their
        // original (older) dates, and paging from the smallest id then
        // re-fetched the same window over and over, duplicating it.
        this.offset = this.messages.length ? this.messages[this.messages.length - 1].id! : 0;
        this.rebuildItems();
        if (opt.messageId && !opt.quiet) {
          const target = opt.messageId;
          if (this.messages.some(m => m.id === target)) {
            setTimeout(() => this.scrollToId({ messageId: target, smooth: false, mark: opt.mark }), 300);
          } else {
            // The one page positioned around it did not hold it: it is gone
            // (deleted, or moved in time by an import). Stop here — this is
            // where the old code asked scrollToId again, which fetched the
            // next page, and the next, through the entire history.
            this.notifyMessageNotFound();
          }
        }
      }
    } catch (error) {
      if (error instanceof HttpErrorResponse && error.status === 404) this.onChannelGone();
      console.error('שגיאה בטעינת הודעות:', error);
      this.lastLoadOk = false;
    } finally {
      this.isLoading = false;
    }
  }

  /**
   * Re-runs the initial history load after it failed. Without this a /messages
   * error on first paint left the feed permanently empty — indistinguishable
   * from an empty channel, with no error and no way back short of a full reload.
   */
  retryInitialLoad(): void {
    this.initialLoadFailed = false;
    this.isVisible = false;
    this.loadMessages({ resetList: true }).then(() => this.revealAfterInitialLoad());
  }
}
