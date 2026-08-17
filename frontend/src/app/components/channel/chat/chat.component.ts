
import { Component, OnInit, NgZone, OnDestroy, HostListener } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbBadgeModule,
  NbButtonModule,
  NbCardModule,
  NbChatModule,
  NbIconModule,
  NbLayoutModule,
  NbListModule,
  NbToastrService
} from "@nebular/theme";
import { MessageComponent } from "./message/message.component";
import { MagnetAdSlotComponent } from "./magnet-ad-slot/magnet-ad-slot.component";
import { firstValueFrom, interval, Subscription } from 'rxjs';
import { ChatMessage, ChatService } from '../../../services/chat.service';
import { AuthService } from '../../../services/auth.service';
import { ActivatedRoute } from '@angular/router';
import { NotificationsService } from '../../../services/notifications.service';
import { User } from '../../../models/user.model';
import { AdminService } from '../../../services/admin.service';
import { MagnetAdsService } from '../../../services/magnet-ads.service';
import { SlugService } from '../../../services/slug.service';

type LoadMsgOpt = {
  scrollDown?: boolean;
  messageId?: number;
  mark?: boolean;
  resetList?: boolean;
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
    FormsModule,
    NbLayoutModule,
    NbChatModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    NbListModule,
    NbBadgeModule,
    MessageComponent,
    MagnetAdSlotComponent
  ],
  templateUrl: './chat.component.html',
  styleUrl: './chat.component.scss'
})
export class ChatComponent implements OnInit, OnDestroy {
  private eventSource!: EventSource;
  private sseEverConnected = false;
  messages: ChatMessage[] = [];
  adSlotsAfter: Set<number> = new Set();
  scheduledMessages!: ChatMessage[];
  hideScheduledMessages: boolean = false;
  userInfo?: User;
  isLoading: boolean = false;
  isOffline: boolean = false;
  isVisible: boolean = false;   // hidden until initial scroll is resolved
  offset: number = 0;
  limit: number = 20;
  hasOldMessages: boolean = true;
  hasNewMessages: boolean = false;
  thereNewMessages: boolean = false;
  showScrollToBottom: boolean = false;
  private lastHeartbeat: number = Date.now();
  private subLastHeartbeat?: Subscription;
  private schedulingSub?: Subscription;
  lastReadMessageId: number = 0;

  constructor(
    private chatService: ChatService,
    private _authService: AuthService,
    private _adminService: AdminService,
    private toastrService: NbToastrService,
    private notificationService: NotificationsService,
    private magnetAds: MagnetAdsService,
    private slugService: SlugService,
    private zone: NgZone,
    private router: ActivatedRoute,
  ) { }

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

  @HostListener('window:scroll', [])
  onWindowScroll() {
    this.onListScroll();
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

  scrollToId(opt: ScrollOpt) {
    const element = document.getElementById(opt.messageId.toString());
    if (element) {
      element.scrollIntoView({ behavior: opt.smooth ? 'smooth' : 'instant', block: 'center' });
      this.removeMsgMarked();
      opt.mark && element.classList.add('mark_message');
    } else {
      this.loadMessages({ scrollDown: false, messageId: opt.messageId, mark: opt.mark });
    }
  }

  private removeMsgMarked() {
    document.querySelectorAll('.mark_message').forEach((el) => {
      el.classList.remove('mark_message');
    });
  }

  ngAfterViewInit(): void {
    setTimeout(() => {
      this.router.fragment.subscribe(fragment => {
        if (fragment) {
          const messageId = Number(fragment);
          if (!Number.isInteger(messageId)) return;
          this.scrollToId({ messageId: messageId, mark: true });
        }
      });
    }, 800);
  }

  ngOnInit() {
    this.schedulingSub = this._adminService.schedulingBusObservable.subscribe(() => {
      this.loadScheduledMessages();
    });

    this.chatService.getEmojisList(true);

    this.magnetAds.loadSettings()
      .catch(() => null)
      .then(() => this.rebuildItems());

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

    this.loadMessages().then(() => {
      const lastReadMsg = Number(localStorage.getItem('lastReadMessage'));
      const lastMsgId = this.messages[0]?.id;
      if (!lastMsgId) { this.isVisible = true; return; }

      if (lastReadMsg && lastReadMsg < lastMsgId) {
        // Set the indicator BEFORE revealing the list so the line renders
        // at the right position on first paint — no visible jump.
        this.lastReadMessageId = lastReadMsg;
        this.scrollToId({ messageId: lastReadMsg, smooth: false, mark: false });
        // Wait one frame for scrollToId to finish (it may need to load more messages),
        // then reveal. setLastReadMessage only after the user has actually seen the
        // position — so a refresh still brings them back.
        setTimeout(() => {
          this.isVisible = true;
          this.setLastReadMessage(lastMsgId.toString());
        }, 350);
      } else {
        this.scrollToBottom(false);
        this.isVisible = true;
        this.setLastReadMessage(lastMsgId.toString());
      }
    });
  }

  async setLastReadMessage(id: string) {
    localStorage.setItem('lastReadMessage', id);
  }

  private initializeMessageListener() {
    this.eventSource = this.chatService.sseListener();

    this.eventSource.onopen = () => {
      if (this.sseEverConnected) {
        // Reconnect after a drop — fetch messages that arrived during the gap
        this.zone.run(() => { this.isOffline = false; });
        this.loadMissedMessages();
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

      const message = JSON.parse(event.data);
      switch (message.type) {
        case 'new-message':
          if (this.hasNewMessages) break;
          if (this.messages.some(m => m.id === message.message.id)) break; // dedup after reconnect
          this.zone.run(() => {
            this.messages.unshift(message.message);
            this.thereNewMessages = !this.isAtBottom() && !(message.message.author === this.userInfo?.username);
            this.setLastReadMessage(message.message.id!.toString());
            if (this.hasWriteRole() && this.scheduledMessages && message.message.author === "Scheduled") {
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
          });
          break;
        case 'edit-message':
          this.zone.run(() => {
            const index = this.messages.findIndex(m => m.id === message.message.id);
            if (index !== -1) {
              this.messages[index] = message.message;
            } else {
              // TOTO: Find the closest message to attach the retrieved message to
              //  const closestIndex = this.messages.reduce
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
    this.chatService.sseClose();
    this.subLastHeartbeat?.unsubscribe();
    this.schedulingSub?.unsubscribe();
  }

  private rebuildItems() {
    try {
      this.adSlotsAfter = this.magnetAds.computeAdSlots(this.messages);
    } catch (e) {
      console.error('computeAdSlots failed, ads will not be shown:', e);
      this.adSlotsAfter = new Set();
    }
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
        }
      });
  }

  private async loadMissedMessages() {
    if (!this.messages.length) return;
    const maxId = Math.max(...this.messages.map(m => m.id!));
    try {
      const missed = await firstValueFrom(this.chatService.getMessages(maxId, this.limit, 'asc'));
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
          this.scrollToBottom(false);
        } else {
          this.thereNewMessages = true;
        }
      });
    } catch {
      // Best-effort; will retry on the next reconnect
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

  onListScroll() {
    const distanceFromBottom = document.documentElement.scrollHeight - window.innerHeight - window.scrollY;
    this.showScrollToBottom = distanceFromBottom > 100;
    if (distanceFromBottom < 10) {
      this.thereNewMessages = false;
    }
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
    if (this.isLoading || (opt.scrollDown && !this.hasNewMessages) || (!opt.scrollDown && !this.hasOldMessages)) return;

    let startId: number;
    let resetList: boolean = opt.resetList || false;
    let direction: string = "desc";

    opt.resetList && (this.offset = 0);

    const maxId = this.messages.length ? Math.max(...this.messages.map(m => m.id!)) : 0;
    if (opt.scrollDown) {
      direction = "asc";
      startId = maxId;
    } else {
      if (opt.messageId) {
        if (opt.messageId > maxId + this.limit) {
          resetList = true;
          this.hasNewMessages = true;
          this.hasOldMessages = true;
          startId = opt.messageId + 10;
          direction = "asc";
          opt.scrollDown = true;
        } else if (opt.messageId > maxId) {
          startId = maxId;
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
        if (opt.scrollDown) {
          resetList ? this.messages = response.reverse() : this.messages.unshift(...response.reverse());
          this.hasNewMessages = response.length >= this.limit;
        } else {
          resetList ? this.messages = response : this.messages.push(...response);
          this.hasOldMessages = response.length >= this.limit;
        }
        this.offset = Math.min(...this.messages.map(m => m.id!));
        this.rebuildItems();
        setTimeout(() => {
          opt.messageId && this.scrollToId({ messageId: opt.messageId, smooth: false, mark: opt.mark });
        }, 300);
      }
    } catch (error) {
      console.error('שגיאה בטעינת הודעות:', error);
    } finally {
      this.isLoading = false;
    }
  }
}
