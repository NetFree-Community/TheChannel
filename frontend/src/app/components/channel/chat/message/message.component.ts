import { AfterViewInit, Component, ElementRef, Input, NgZone, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { KeyValuePipe } from "@angular/common";
import {
  NbButtonModule,
  NbDialogService,
  NbIconModule,
  NbPopoverDirective,
  NbPopoverModule,
  NbPosition,
  NbToastrService,
  NbTooltipModule
} from "@nebular/theme";
import { MarkdownComponent } from "ngx-markdown";
import type Viewer from 'viewerjs';
import { MessageTimePipe, formatFullDateTime } from '../../../../pipes/message-time.pipe';
import { ChatMessage, ChatService } from '../../../../services/chat.service';
import { AdminService } from '../../../../services/admin.service';
import { AuthService } from '../../../../services/auth.service';
import { SlugService } from '../../../../services/slug.service';
import { ConfirmService } from '../../../../services/confirm.service';
import { ShareService } from '../../../../services/share.service';
import { ReportComponent } from './report/report.component';
import { EMBED_PREFIX_REGEX } from '../../../../markdown.config';

// One window scroll listener shared by every card on the page. Each card used
// to register its own (plus its own timer), so a flick over a long feed ran
// dozens of handlers and dozens of change-detection passes per scroll event,
// all to keep the hover-opened emoji row shut while the page moves.
const scrollState = { scrolling: false, timer: undefined as any, refs: 0 };
const onWindowScroll = () => {
  scrollState.scrolling = true;
  clearTimeout(scrollState.timer);
  scrollState.timer = setTimeout(() => { scrollState.scrolling = false; }, 150);
};
function acquireScrollTracking(zone: NgZone) {
  if (scrollState.refs++ > 0) return;
  // Outside the zone: the flag is only read on hover, nothing needs repainting.
  zone.runOutsideAngular(() => window.addEventListener('scroll', onWindowScroll, { capture: true, passive: true }));
}
function releaseScrollTracking() {
  if (--scrollState.refs > 0) return;
  window.removeEventListener('scroll', onWindowScroll, true);
  clearTimeout(scrollState.timer);
  scrollState.scrolling = false;
}

@Component({
  selector: 'app-message',
  imports: [
    KeyValuePipe,
    NbIconModule,
    NbButtonModule,
    NbPopoverModule,
    NbTooltipModule,
    MessageTimePipe,
    MarkdownComponent,
  ],
  templateUrl: './message.component.html',
  styleUrl: './message.component.scss'
})

export class MessageComponent implements OnInit, AfterViewInit, OnDestroy {
  protected readonly NbPosition = NbPosition;

  @Input()
  message: ChatMessage | undefined;

  @Input()
  isSchedulingMessage: boolean = false;

  @Input()
  indexId: number | undefined;

  @ViewChild('actionsMenu') actionsMenu?: NbPopoverDirective;
  @ViewChild('media') mediaContainer!: ElementRef;

  private viewer: Viewer | null = null;
  private target: HTMLElement | null = null;

  constructor(
    private _adminService: AdminService,
    private dialogService: NbDialogService,
    protected chatService: ChatService,
    private toastrService: NbToastrService,
    public _authService: AuthService,
    private slugService: SlugService,
    private confirm: ConfirmService,
    private share: ShareService,
    private host: ElementRef<HTMLElement>,
    private zone: NgZone,
  ) { }

  reacts: string[] = [];
  /** The inline emoji row under the card is open. */
  emojiOpen = false;
  private closeEmojiMenuTimeout: any;
  private hoverTimer: any;
  private get isScrolling(): boolean { return scrollState.scrolling; }
  private readonly minimalHoverMs = 200;
  // Shared with the renderer so the two can never disagree about what an embed
  // token looks like — a private copy here missed the optional size suffix.
  private readonly matchFindCustomEmbedReg = EMBED_PREFIX_REGEX;

  private get channelRole(): string | undefined {
    return this._authService.userInfo?.channelRoles?.[this.slugService.slug];
  }

  get canWrite(): boolean {
    const user = this._authService.userInfo;
    if (!user) return false;
    if (user.globalRole === 'super_admin') return true;
    const role = this.channelRole;
    return role === 'owner' || role === 'moderator' || role === 'writer';
  }

  get canModerate(): boolean {
    const user = this._authService.userInfo;
    if (!user) return false;
    if (user.globalRole === 'super_admin') return true;
    const role = this.channelRole;
    return role === 'owner' || role === 'moderator';
  }

  /** Whether the card shows the "add reaction" chip at all. */
  get canOfferReactions(): boolean {
    return this.chatService.reactionsEnabled && this.reacts.length > 0
      && !this.isSchedulingMessage && !this.message?.is_ads;
  }

  /** Any menu entry at all? Readers who may only copy the link still get the menu. */
  get hasActions(): boolean {
    return !this.isSchedulingMessage || this.canModify(this.message!);
  }

  /** Count of all reactions, for the hidden-text summary. */
  get reactionCount(): number {
    const r = this.message?.reactions;
    return r ? Object.values(r).reduce((a, b) => a + (b || 0), 0) : 0;
  }

  /** The exact moment behind the relative label ("אתמול 14:05"), for the tooltip. */
  get fullTime(): string {
    const d = this.message?.timestamp ? new Date(this.message.timestamp as any) : null;
    return d && !isNaN(d.getTime()) ? formatFullDateTime(d) : '';
  }

  get isoTime(): string | null {
    const d = this.message?.timestamp ? new Date(this.message.timestamp as any) : null;
    return d && !isNaN(d.getTime()) ? d.toISOString() : null;
  }

  /** The OS share sheet exists (phones); shown as one more way to pass a post on. */
  get canShareSheet(): boolean {
    return this.share.canShare;
  }

  ngOnInit() {
    this.chatService.getEmojisList()
      .then(emojis => this.reacts = emojis)
      .catch(() => this.toastrService.danger('', 'שגיאה בהגדרת אימוגים'));

    acquireScrollTracking(this.zone);
  }

  ngOnDestroy() {
    releaseScrollTracking();
    this.cancelEmojiMenuClose();
    this.clearHoverTimer();
    this.unlistenOutside();
    if (this.viewer) {
      this.viewer.destroy();
      this.viewer = null;
    }
  }

  ngAfterViewInit(): void {
    // Decide the auth-overlay from resolved state instead of racing a fixed
    // timer against the channel-info and user-info requests.
    Promise.all([
      this.chatService.ensureChannelInfo().catch(() => null),
      this._authService.loadUserInfo().catch(() => null),
    ]).then(() => {
      if (!this.chatService.channelInfo?.require_auth_for_view_files || this._authService.userInfo) return;
      const media = this.mediaContainer?.nativeElement.querySelectorAll('img, video');
      media?.forEach((item: HTMLMediaElement) => {
        {
          // Sized by CSS (.auth-overlay-wrapper), not copied from the media
          // element: an image the server refuses (401) never loads, and legacy
          // ones carry no height attribute, so both measured 0px here and the
          // call-to-action collapsed to nothing — a broken picture and no hint
          // that logging in would fix it.
          const wrapper = document.createElement('div');
          wrapper.className = 'auth-overlay-wrapper';

          const overlay = document.createElement('div');
          overlay.className = 'auth-overlay';
          overlay.setAttribute('role', 'button');
          overlay.setAttribute('tabindex', '0');
          overlay.innerHTML = '<div class="auth-overlay__text">יש להתחבר כדי לצפות בקבצים<br>לחצו כאן להתחברות</div>';

          const login = () => {
            // Same as the header's login button: the login page returns to
            // returnUrl, and with none recorded a reader who signed in from
            // here landed on /channel (my-channel / onboarding) instead of
            // the feed they were reading. Written here, not in
            // loginWithGoogle(), so the /login page's own button does not
            // overwrite the guard's returnUrl with '/login'.
            try {
              localStorage.setItem('returnUrl', location.pathname + location.search + location.hash);
            } catch {
              // Storage unavailable — the login page falls back to /channel.
            }
            this._authService.loginWithGoogle();
          };
          overlay.addEventListener('click', login);
          overlay.addEventListener('keydown', (e: KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); login(); }
          });

          const parent = item.parentElement;
          if (parent) {
            parent.replaceChild(wrapper, item);
            wrapper.appendChild(item);
            wrapper.appendChild(overlay);
          }
        }
      });
    });
  }

  // Mirrors backend canModifyMessage: moderators+ on anything, writers only on
  // their own posts or system posts (authorId "" / "0").
  canModify(message: ChatMessage): boolean {
    if (this.canModerate) return true;
    if (!this.canWrite) return false;
    const a = message.authorId;
    return a === '' || a === '0' || a === undefined || a === this._authService.userInfo?.id;
  }

  /** Closes the kebab menu before an action runs, so the overlay never lingers. */
  private closeActions() {
    this.actionsMenu?.hide();
  }

  /**
   * The popover renders into the overlay container at the end of the page and
   * manages no focus of its own, so after Enter on the kebab, Tab would have
   * carried on through the feed and the actions were mouse-only. Focus goes
   * into the menu when it opens and back to the kebab when it closes — unless
   * the user has already clicked somewhere else, which must not be undone.
   */
  onActionsState(shown: boolean) {
    if (shown) {
      setTimeout(() => document.querySelector<HTMLElement>('.msg-actions .msg-actions__item')?.focus());
      return;
    }
    const active = document.activeElement;
    if (!active || active === document.body || active.closest('.msg-actions')) {
      this.host.nativeElement.querySelector<HTMLElement>('.msg__kebab')?.focus();
    }
  }

  onActionsKeydown(event: KeyboardEvent) {
    const menu = event.currentTarget as HTMLElement;
    const items = Array.from(menu.querySelectorAll<HTMLElement>('.msg-actions__item'));
    const at = items.indexOf(document.activeElement as HTMLElement);
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp': {
        event.preventDefault();
        const step = event.key === 'ArrowDown' ? 1 : -1;
        items[(at + step + items.length) % items.length]?.focus();
        break;
      }
      case 'Home':
      case 'End':
        event.preventDefault();
        items[event.key === 'Home' ? 0 : items.length - 1]?.focus();
        break;
      case 'Escape':
        event.preventDefault();
        event.stopPropagation();
        this.closeActions();
        break;
      case 'Tab':
        // Tab leaves the menu; close it rather than leave an open overlay behind.
        this.closeActions();
        break;
    }
  }

  editMessage(message: ChatMessage) {
    this.closeActions();
    // A scheduled entry is handed over as the object itself: the service finds
    // it again in the server's list by time and text (locateScheduled), so
    // nothing is stamped on it. The index it used to carry went stale as soon
    // as the list was reloaded behind the open editor (every SSE reconnect
    // does that) and then addressed a neighbour.
    this._adminService.setEditMessage({ message, isScheduling: this.isSchedulingMessage });
  }

  async deleteMessage(message: ChatMessage) {
    this.closeActions();
    const ok = await this.confirm.ask({
      title: this.isSchedulingMessage ? 'למחוק את ההודעה המתוזמנת?' : 'למחוק את ההודעה?',
      message: this.isSchedulingMessage
        ? 'ההודעה תימחק מרשימת ההודעות המתוזמנות ולא תתפרסם.'
        : 'ההודעה תיעלם לקוראים. אפשר לשחזר אותה מהתפריט.',
      status: 'danger',
      confirmLabel: 'מחיקה',
    });
    if (!ok) return;
    if (this.isSchedulingMessage) {
      // The service commits a new list only after the server accepted it,
      // so the feed's copy has to be refreshed from it; it no longer shares
      // the array that used to be spliced in place.
      this._adminService.deleteScheduledMessage(message)
        .then(() => this._adminService.reloadSchedulingMessage())
        .catch(() => this.toastrService.danger('', 'שגיאה במחיקת ההודעה'));
      return;
    }
    this._adminService.deleteMessage(message.id).subscribe({
      next: (res) => {
        if (!res?.success) this.toastrService.danger('', 'שגיאה במחיקת ההודעה');
      },
      error: () => this.toastrService.danger('', 'שגיאה במחיקת ההודעה'),
    });
  }

  openReportDialog(messageId?: number) {
    this.closeActions();
    if (this.isSchedulingMessage) return;
    this.dialogService.open(ReportComponent, { closeOnBackdropClick: true, context: { messageId } });
  }

  quoteMessage(message: ChatMessage) {
    this.closeActions();
    if (this.isSchedulingMessage) return;
    let newMsgText: string | undefined = message.text?.trimStart();
    let fintEmbedded = newMsgText?.match(this.matchFindCustomEmbedReg);
    if (fintEmbedded) {
      switch (fintEmbedded[1]) {
        case 'video':
          newMsgText = "וידיאו 📹";
          break;
        case 'audio':
          newMsgText = "אודיו 🎙️";
          break;
        case 'image':
          newMsgText = "תמונה 📷";
          break;
        case 'quote':
          newMsgText = "ציטוט 💬";
          break;
      }
    }
    // Parentheses go too: the quote token is `[quote-embedded#](id@text)`, and
    // a ')' inside the text closed it early in the tokenizer, leaking the rest
    // of the quote into the message as plain text.
    // A markdown link collapses to its label first: the parenthesis strip
    // below, or the 100-char cut, otherwise left half a link in the quote.
    newMsgText = newMsgText?.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1').slice(0, 100).replaceAll('>', '').replaceAll(/\n/g, ' ').replaceAll('*', '').replaceAll(/[()]/g, '');
    if (message.text && message.text.length > 100) {
      newMsgText += '...';
    }

    const m = this._adminService.getEditMessage();
    if (m?.new || !m?.message) {
      let newMessage: ChatMessage = {
        text: `[quote-embedded#](${message.id}@${newMsgText})\n`
      }
      this._adminService.setEditMessage({ new: true, message: newMessage, isScheduling: this.isSchedulingMessage });
    } else {
      // The composer prepends it to the text on screen; the entry itself is
      // left as the server knows it (see EditMsg.prepend).
      this._adminService.setEditMessage({ ...m, prepend: `[quote-embedded#](${message.id}@${newMsgText})\n` });
    }
  }

  async viewLargeImage(event: MouseEvent) {
    const target = event.target as HTMLElement;

    if (target.tagName === 'IMG' || target.tagName === 'I') {
      const youtubeId = target.getAttribute('youtubeid');
      if (youtubeId) {
        // The player (and @angular/youtube-player behind it) is fetched on the
        // first click only; most readers never open a video.
        const { YoutubePlayerComponent } = await import('../youtube-player/youtube-player.component');
        this.dialogService.open(YoutubePlayerComponent, { closeOnBackdropClick: true, context: { videoId: youtubeId } })
        return;
      }

      if (this.target === target && this.viewer) {
        this.viewer.show();
      } else {
        // Same for the image viewer: loaded when a picture is first tapped.
        const { default: ViewerCtor } = await import('viewerjs');
        if (this.viewer) {
          this.viewer.destroy();
          this.viewer = null;
        }
        this.viewer = new ViewerCtor(target, {
          toolbar: false,
          transition: true,
          navbar: false,
          title: false
        });
        this.target = target;
        this.viewer.show();
      }
    }
  }

  setReact(id: number | undefined, react: string) {
    if (this.isSchedulingMessage) return;
    if (!this._authService.userInfo) {
      this.toastrService.danger('', "כדי להגיב באימוג'י יש להתחבר לחשבון");
      return;
    }
    if (id && react)
      this.chatService.setReact(id, react).catch(() => this.toastrService.danger('', "הייתה בעיה, נסו שנית."));
  }

  /** Tap on the "add reaction" chip: open or close the emoji row. */
  toggleEmojiMenu() {
    this.clearHoverTimer();
    if (this.emojiOpen) {
      this.closeEmojiMenu();
      return;
    }
    if (!this._authService.userInfo) {
      this.toastrService.danger('', "כדי להגיב באימוג'י יש להתחבר לחשבון");
      return;
    }
    this.openEmojiMenu();
  }

  /** Desktop convenience: hovering the chip opens the row after a short delay. */
  showEmojiMenu() {
    if (!this._authService.userInfo || this.isScrolling || this.message?.is_ads || this.isSchedulingMessage) return;
    // The channel operator can switch reactions off; the backend answers 403.
    if (!this.chatService.reactionsEnabled) return;
    this.clearHoverTimer();
    this.hoverTimer = setTimeout(() => {
      if (!this.isScrolling) {
        this.cancelEmojiMenuClose();
        this.openEmojiMenu();
      }
    }, this.minimalHoverMs);
  }

  private openEmojiMenu() {
    if (!this.canOfferReactions) return;
    this.emojiOpen = true;
    this.listenOutside();
  }

  private closeEmojiMenu() {
    this.emojiOpen = false;
    this.unlistenOutside();
  }

  /** Escape anywhere on the card shuts the emoji row, like any other popup. */
  onEscape() {
    if (this.emojiOpen) this.closeEmojiMenu();
  }

  pickReact(react: string) {
    this.setReact(this.message?.id, react);
    this.closeEmojiMenu();
  }

  scheduleEmojiMenuClose() {
    this.clearHoverTimer();
    this.closeEmojiMenuTimeout = setTimeout(() => {
      this.closeEmojiMenu();
    }, 150);
  }

  cancelEmojiMenuClose() {
    this.clearHoverTimer();
    if (this.closeEmojiMenuTimeout) {
      clearTimeout(this.closeEmojiMenuTimeout);
      this.closeEmojiMenuTimeout = undefined;
    }
  }

  clearHoverTimer() {
    if (this.hoverTimer) {
      clearTimeout(this.hoverTimer);
      this.hoverTimer = undefined;
    }
  }

  // A document listener only while the row is open (one per open row, not one
  // per message on the page): a tap anywhere outside this card closes it.
  private outsideListener?: (e: Event) => void;

  private listenOutside() {
    if (this.outsideListener) return;
    this.outsideListener = (e: Event) => {
      if (!this.host.nativeElement.contains(e.target as Node)) this.closeEmojiMenu();
    };
    // Deferred so the click that opened the row is not the one that closes it.
    setTimeout(() => {
      if (this.outsideListener) document.addEventListener('click', this.outsideListener, true);
    });
  }

  private unlistenOutside() {
    if (this.outsideListener) {
      document.removeEventListener('click', this.outsideListener, true);
      this.outsideListener = undefined;
    }
  }

  isEdited(message: ChatMessage): boolean {
    if (!message.last_edit) return false;
    // "Never edited" arrives as Go's zero time, 0001-01-01T00:00:00Z. Judged
    // by the *local* year that is year 0 anywhere west of UTC, so every
    // unedited message read "נערכה 31/12/0000" for viewers in the Americas.
    // The zero time sits ~62 billion seconds below the epoch, so an epoch
    // test needs no timezone — and NaN (an unparsable value) fails it too.
    return new Date(message.last_edit).getTime() > 0;
  }

  private messageUrl(messageId: number): string {
    return `${window.location.origin}/channel/${this.slugService.slug}#${messageId}`;
  }

  async copyLink(messageId?: number) {
    this.closeActions();
    if (!messageId || this.isSchedulingMessage) return;
    // ShareService explains a blocked clipboard itself.
    if (await this.share.copy(this.messageUrl(messageId))) {
      this.toastrService.success('', 'הקישור להודעה הועתק');
    }
  }

  /** The OS share sheet with the post's link; falls back to the clipboard. */
  async shareMessage(messageId?: number) {
    this.closeActions();
    if (!messageId || this.isSchedulingMessage) return;
    const url = this.messageUrl(messageId);
    const outcome = await this.share.share({ title: this.chatService.channelInfo?.name || '', url });
    if (outcome === 'unsupported' && await this.share.copy(url)) {
      this.toastrService.success('', 'הקישור להודעה הועתק');
    }
  }
}
