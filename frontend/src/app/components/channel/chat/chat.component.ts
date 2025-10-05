import { CommonModule } from '@angular/common';
import { Component, OnInit, NgZone, OnDestroy, HostListener } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  NbBadgeModule,
  NbButtonModule,
  NbCardModule,
  NbChatModule,
  NbIconModule,
  NbLayoutModule,
  NbListModule
} from "@nebular/theme";
import { MessageComponent } from "./message/message.component";
import { firstValueFrom, interval } from 'rxjs';
import { ChatMessage, ChatService } from '../../../services/chat.service';
import { AuthService } from '../../../services/auth.service';
import { ActivatedRoute } from '@angular/router';
import { NotificationsService } from '../../../services/notifications.service';
import { User } from '../../../models/user.model';

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
    CommonModule,
    FormsModule,
    NbLayoutModule,
    NbChatModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    NbListModule,
    NbBadgeModule,
    MessageComponent,
  ],
  templateUrl: './chat.component.html',
  styleUrl: './chat.component.scss'
})
export class ChatComponent implements OnInit, OnDestroy {
  private eventSource!: EventSource;
  messages: ChatMessage[] = [];
  userInfo?: User;
  isLoading: boolean = false;
  offset: number = 0;
  limit: number = 20;
  hasOldMessages: boolean = true;
  hasNewMessages: boolean = false;
  thereNewMessages: boolean = false;
  showScrollToBottom: boolean = false;
  private lastHeartbeat: number = Date.now();
  private subLastHeartbeat: any;
  lastReadMessageId: number = 0;

  constructor(
    private chatService: ChatService,
    private _authService: AuthService,
    private notificationService: NotificationsService,
    private zone: NgZone,
    private router: ActivatedRoute,
  ) { }

  @HostListener('window:scroll', [])
  onWindowScroll() {
    this.onListScroll();
  }

  @HostListener('document:keydown', ['$event'])
  @HostListener('window:click', ['$event'])
  onUserAction(event: MouseEvent) {
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
    this.chatService.getEmojisList(true);

    this.initializeMessageListener();
    this.keepAliveSSE();

    this._authService.loadUserInfo().then((res) => {
      this.userInfo = res;
      this.notificationService.init();
    });

    this.loadMessages().then(() => {
      const lastReadMsg = Number(localStorage.getItem('lastReadMessage'));
      const lastMsgId = this.messages[0].id!;
      if (lastReadMsg && lastReadMsg < lastMsgId) {
        setTimeout(() => {
          this.scrollToId({ messageId: lastReadMsg, smooth: false, mark: false });
          this.lastReadMessageId = lastReadMsg;
        }, 200);
      } else {
        this.scrollToBottom(false);
      }

      this.setLastReadMessage(lastMsgId.toString());
    });
  }

  async setLastReadMessage(id: string) {
    localStorage.setItem('lastReadMessage', id);
  }

  private async initializeMessageListener() {
    this.eventSource = this.chatService.sseListener();
    this.eventSource.onmessage = (event) => {

      this.lastHeartbeat = Date.now();

      const message = JSON.parse(event.data);
      switch (message.type) {
        case 'new-message':
          if (this.hasNewMessages) break;
          this.zone.run(() => {
            this.messages.unshift(message.message);
            this.thereNewMessages = !(message.message.author === this.userInfo?.username);
            this.setLastReadMessage(message.message.id!.toString());
          });
          break;
        case 'delete-message':
          if (this.userInfo?.privileges?.['writer']) {
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
    clearInterval(this.subLastHeartbeat);
  }

  async keepAliveSSE() {
    clearInterval(this.subLastHeartbeat);
    this.subLastHeartbeat = interval(10000)
      .subscribe(() => {
        if (Date.now() - this.lastHeartbeat > 60000) {
          this.lastHeartbeat = Date.now();
          this.initializeMessageListener();
        };
      });
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


  async loadMessages(opt: LoadMsgOpt = {}) {
    if (this.isLoading || (opt.scrollDown && !this.hasNewMessages) || (!opt.scrollDown && !this.hasOldMessages)) return;

    let startId: number;
    let resetList: boolean = opt.resetList || false;
    let direction: string = "desc";

    opt.resetList && (this.offset = 0);

    const maxId = Math.max(...this.messages.map(m => m.id!));
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
