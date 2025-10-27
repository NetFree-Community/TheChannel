import { AfterViewInit, Component, ElementRef, HostListener, Input, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { NgIf, CommonModule } from "@angular/common";
import {
  NbButtonModule,
  NbCardModule, NbChatModule,
  NbDialogService,
  NbIconModule,
  NbPopoverModule,
  NbPosition,
  NbToastrService, NbUserModule
} from "@nebular/theme";
import { MarkdownComponent } from "ngx-markdown";
import Viewer from 'viewerjs';
import { YoutubePlayerComponent } from '../youtube-player/youtube-player.component';
import { NgbPopover, NgbPopoverModule } from '@ng-bootstrap/ng-bootstrap';
import { MessageTimePipe } from '../../../../pipes/message-time.pipe';
import { ChatMessage, ChatService } from '../../../../services/chat.service';
import { AdminService } from '../../../../services/admin.service';
import { AuthService } from '../../../../services/auth.service';
import { ReportComponent } from './report/report.component';
@Component({
  selector: 'app-message',
  imports: [
    NgIf,
    CommonModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    MessageTimePipe,
    MarkdownComponent,
    NbPopoverModule,
    NgbPopoverModule,
    NbChatModule,
    NbUserModule,
  ],
  templateUrl: './message.component.html',
  styleUrl: './message.component.scss'
})

export class MessageComponent implements OnInit, AfterViewInit, OnDestroy {
  protected readonly NbPosition = NbPosition;

  @Input()
  message: ChatMessage | undefined;

  @ViewChild(NgbPopover) popover!: NgbPopover;
  @ViewChild('media') mediaContainer!: ElementRef;

  private viewer: Viewer | null = null;
  private target: HTMLElement | null = null;

  constructor(
    private _adminService: AdminService,
    private dialogService: NbDialogService,
    protected chatService: ChatService,
    private toastrService: NbToastrService,
    public _authService: AuthService,
  ) { }

  reacts: string[] = [];
  private closeEmojiMenuTimeout: any;
  private isScrolling = false;
  private hoverTimer: any;
  private readonly minimalHoverMs = 200;
  private readonly matchFindCustomEmbedReg = /^\[(video|audio|image|quote)-embedded#].*/;

  ngOnInit() {
    this.chatService.getEmojisList()
      .then(emojis => this.reacts = emojis)
      .catch(() => this.toastrService.danger('', 'שגיאה בהגדרת אימוגים'));

    window.addEventListener('scroll', this.onScroll, true);
  }

  ngOnDestroy() {
    window.removeEventListener('scroll', this.onScroll, true);
    this.cancelEmojiMenuClose();
    this.clearHoverTimer();
    if (this.viewer) {
      this.viewer.destroy();
      this.viewer = null;
    }
  }

  private scrollTimeout: any;

  onScroll = () => {
    this.isScrolling = true;
    if (this.scrollTimeout) {
      clearTimeout(this.scrollTimeout);
    }
    this.scrollTimeout = setTimeout(() => {
      this.isScrolling = false;
      this.scrollTimeout = undefined;
    }, 150)
  };

  ngAfterViewInit(): void {
    setTimeout(() => {
      const media = this.mediaContainer?.nativeElement.querySelectorAll('img, video');
      media?.forEach((item: HTMLMediaElement) => {
        if (this.chatService.channelInfo?.require_auth_for_view_files && !this._authService.userInfo) {
          const wrapper = document.createElement('div');
          wrapper.style.position = 'relative';
          wrapper.style.display = 'inline-block';
          wrapper.style.width = item.offsetWidth + 'px';
          wrapper.style.height = item.offsetHeight + 'px';

          const overlay = document.createElement('div');
          overlay.style.position = 'absolute';
          overlay.style.top = '0';
          overlay.style.left = '0';
          overlay.style.width = '100%';
          overlay.style.height = '100%';
          overlay.style.background = 'rgba(0,0,0,0.5)';
          overlay.style.backdropFilter = 'blur(4px)';
          overlay.style.display = 'flex';
          overlay.style.alignItems = 'center';
          overlay.style.justifyContent = 'center';
          overlay.style.color = 'white';
          overlay.style.fontSize = '14px';
          overlay.style.cursor = 'pointer';
          overlay.style.zIndex = '1';
          overlay.innerHTML = '<div style="text-align: center;">יש להתחבר כדי לצפות בקבצים <br>לחצו כאן להתחברות</div>';

          overlay.addEventListener('click', () => {
            this._authService.loginWithGoogle();
          });

          const parent = item.parentElement;
          if (parent) {
            parent.replaceChild(wrapper, item);
            wrapper.appendChild(item);
            wrapper.appendChild(overlay);
          }
        }
      });
    }, 1000);
  }

  editMessage(message: ChatMessage) {
    this._adminService.setEditMessage({ message });
  }

  deleteMessage(message: ChatMessage) {
    const confirm = window.confirm('האם אתה בטוח שברצונך למחוק את ההודעה?');
    if (confirm)
      this._adminService.deleteMessage(message.id).subscribe();
  }

  openReportDialog(messageId?: number) {
    if (!messageId) return;
    this.dialogService.open(ReportComponent, { closeOnBackdropClick: true, context: { messageId } });
  }

  quoteMessage(message: ChatMessage) {
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
    newMsgText = newMsgText?.slice(0, 100).replace('>', '').replaceAll(/\n/g, ' ').replaceAll('*', '');
    if (message.text && message.text.length > 100) {
      newMsgText += '...';
    }

    const m = this._adminService.getEditMessage();
    if (m?.new || !m?.message) {
      let newMessage: ChatMessage = {
        text: `[quote-embedded#](${message.id}@${newMsgText})\n`
      }
      this._adminService.setEditMessage({ new: true, message: newMessage });
    } else {
      m.message.text = `[quote-embedded#](${message.id}@${newMsgText})\n${m.message.text}`;
      this._adminService.setEditMessage(m);
    }
  }

  viewLargeImage(event: MouseEvent) {
    const target = event.target as HTMLElement;

    if (target.tagName === 'IMG' || target.tagName === 'I') {
      const youtubeId = target.getAttribute('youtubeid');
      if (youtubeId) {
        this.dialogService.open(YoutubePlayerComponent, { closeOnBackdropClick: true, context: { videoId: youtubeId } })
        return;
      }

      if (this.target === target && this.viewer) {
        this.viewer.show();
      } else {
        if (this.viewer) {
          this.viewer.destroy();
          this.viewer = null;
        }
        this.viewer = new Viewer(target, {
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
    if (!this._authService.userInfo) {
      this.toastrService.danger('', "יש להתחבר לחשבון בכדי להוסיף אימוג'ים");
      return;
    }
    if (id && react)
      this.chatService.setReact(id, react).catch(() => this.toastrService.danger('', "הייתה בעיה, נסו שנית."));
  }

  showEmojiMenu() {
    if (!this._authService.userInfo || this.isScrolling || this.message?.is_ads) return;
    this.clearHoverTimer();
    this.hoverTimer = setTimeout(() => {
      if (!this.isScrolling) {
        this.cancelEmojiMenuClose();
        this.popover.open();
      }
    }, this.minimalHoverMs);
  }

  scheduleEmojiMenuClose() {
    this.clearHoverTimer();
    this.closeEmojiMenuTimeout = setTimeout(() => {
      this.popover.close();
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


  isEdited(message: ChatMessage): boolean {
    if (!message.last_edit) return false;
    const date = new Date(message.last_edit).getFullYear();
    if (isNaN(date)) return false;
    return date !== 1;
  }

  copyLink(messageId?: number) {
    if (!messageId) return;
    const url = `${window.location.origin}/#${messageId}`;
    navigator.clipboard.writeText(url).then(() => {
      this.toastrService.success('', 'הקישור הועתק ללוח');
    });
  }

  needOpacity(): boolean {
    return this.message ? this.message.deleted || (this.message.timestamp ? new Date(this.message.timestamp) > new Date() : false) : false;
  }
}
