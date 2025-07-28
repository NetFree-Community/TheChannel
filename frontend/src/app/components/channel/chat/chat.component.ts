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
import { AuthService, User } from '../../../services/auth.service';
import { InputFormComponent } from "./input-form/input-form.component";

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
    InputFormComponent
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
  hasMoreMessages: boolean = true;
  hasNewMessages: boolean = false;
  showScrollToBottom: boolean = false;
  private lastHeartbeat: number = Date.now();
  private subLastHeartbeat: any;

  constructor(
    private chatService: ChatService,
    private _authService: AuthService,
    private zone: NgZone,
  ) { }

  @HostListener('window:scroll', [])
  onWindowScroll() {
    this.onListScroll();
  }

  ngOnInit() {
    this.chatService.getEmojisList(true);

    this.initializeMessageListener();
    this.keepAliveSSE();

    this._authService.loadUserInfo().then(res => this.userInfo = res);

    this.loadMessages().then(() => {
      this.scrollToBottom();
    });
  }

  private async initializeMessageListener() {
    this.eventSource = this.chatService.sseListener();
    this.eventSource.onmessage = (event) => {
      const message = JSON.parse(event.data);
      switch (message.type) {
        case 'new-message':
          this.zone.run(() => {
            this.messages.unshift(message.message);
            this.hasNewMessages = !(message.message.author === this.userInfo?.username);
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
    this.subLastHeartbeat = interval(3000)
      .subscribe(() => {
        if (Date.now() - this.lastHeartbeat > 28000) {
          this.lastHeartbeat = Date.now();
          this.initializeMessageListener();
        };
      });
  }

  onListScroll() {
    const distanceFromBottom = document.documentElement.scrollHeight - window.innerHeight - window.scrollY;
    this.showScrollToBottom = distanceFromBottom > 100;
    if (distanceFromBottom < 10) {
      this.hasNewMessages = false;
    }
  }

  scrollToBottom() {
    setTimeout(() => {
      window.scrollTo({ top: document.body.scrollHeight, behavior: 'smooth' });
    }, 200);
    this.hasNewMessages = false;
  }

  async loadMessages() {
    if (this.isLoading || !this.hasMoreMessages) return;

    try {
      this.isLoading = true;
      const response = await firstValueFrom(this.chatService.getMessages(this.offset, this.limit))
      if (response?.messages) {
        this.hasMoreMessages = response.hasMore;
        this.messages.push(...response.messages);
        this.offset = Math.min(...this.messages.map(m => m.id!));
      }
    } catch (error) {
      console.error('שגיאה בטעינת הודעות:', error);
    } finally {
      this.isLoading = false;
    }
  }
}
