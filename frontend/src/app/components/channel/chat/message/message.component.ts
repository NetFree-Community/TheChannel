import { AfterViewInit, Component, ElementRef, Input, OnInit, ViewChild } from '@angular/core';
import { NgIf, CommonModule } from "@angular/common";
import {
  NbButtonModule,
  NbCardModule,
  NbContextMenuModule, NbDialogService,
  NbIconModule, NbMenuService,
  NbPopoverModule,
  NbPosition,
  NbToastrService
} from "@nebular/theme";
import { filter } from "rxjs";
import { InputFormComponent } from "../input-form/input-form.component";
import { MarkdownComponent } from "ngx-markdown";
import Viewer from 'viewerjs';
import { YoutubePlayerComponent } from '../youtube-player/youtube-player.component';
import { NgbPopover, NgbPopoverModule } from '@ng-bootstrap/ng-bootstrap';
import { MessageTimePipe } from '../../../../pipes/message-time.pipe';
import { ChatMessage, ChatService } from '../../../../services/chat.service';
import { AdminService } from '../../../../services/admin.service';
import { AuthService, User } from '../../../../services/auth.service';
import { Router } from '@angular/router';

@Component({
  selector: 'app-message',
  imports: [
    NgIf,
    CommonModule,
    NbCardModule,
    NbIconModule,
    NbButtonModule,
    MessageTimePipe,
    NbContextMenuModule,
    MarkdownComponent,
    NbPopoverModule,
    NgbPopoverModule,
  ],
  templateUrl: './message.component.html',
  styleUrl: './message.component.scss'
})

export class MessageComponent implements OnInit, AfterViewInit {

  private v!: Viewer;

  protected readonly NbPosition = NbPosition;

  @Input()
  message: ChatMessage | undefined;

  @Input()
  userPrivilege: Record<string, boolean> | undefined = {};

  @Input()
  userInfo?: User;

  @ViewChild(NgbPopover) popover!: NgbPopover;
  @ViewChild('media') mediaContainer!: ElementRef;

  optionsMenu = [ 
    {
      title: 'עריכה',
      icon: 'edit',
      click: (message: ChatMessage) => this.editMessage(message),
      hidden: false
    },
    {
      title: 'מחיקה',
      icon: 'trash',
      click: (message: ChatMessage) => this.deleteMessage(message),
      hidden: false
    }
  ];

  constructor(
    private _adminService: AdminService,
    private menuService: NbMenuService,
    private dialogService: NbDialogService,
    private _chatService: ChatService,
    private toastrService: NbToastrService,
    private _authService: AuthService,
    private router: Router
  ) { }

  reacts: string[] = [];
  private closeEmojiMenuTimeout: any;

  ngOnInit() {
    this.menuService.onItemClick().pipe(
      filter(value => value.tag == this.message?.id?.toString())
    ).subscribe((event) => {
      let item = this.optionsMenu.find(value => {
        return value.title == event.item.title;
      });
      if (item && this.message) {
        item.click(this.message);
      }
    });

    this._chatService.getEmojisList()
      .then(emojis => this.reacts = emojis)
      .catch(() => this.toastrService.danger('', 'שגיאה בהגדרת אימוגים'));
  }

  ngAfterViewInit(): void {
    setTimeout(() => {
      const media = this.mediaContainer?.nativeElement.querySelectorAll('img, video');
      media?.forEach((item: HTMLMediaElement) => {
        if (this._chatService.channelInfo?.require_auth_for_view_files && !this._authService.userInfo) {
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
            this.router.navigate(['/login']);
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
    this.dialogService.open(InputFormComponent, { closeOnBackdropClick: false, context: { message: message } }).onClose
      .subscribe((result: ChatMessage | undefined) => {
        if (result && this.message?.id == result.id) {
          this.message = result;
        }
      });
  }

  deleteMessage(message: ChatMessage) {
    const confirm = window.confirm('האם אתה בטוח שברצונך למחוק את ההודעה?');
    if (confirm)
      this._adminService.deleteMessage(message.id).subscribe();
  }

  viewLargeImage(event: MouseEvent) {
    const target = event.target as HTMLElement;

    if (target.tagName === 'IMG' || target.tagName === 'I') {
      const youtubeId = target.getAttribute('youtubeid');
      if (youtubeId) {
        this.dialogService.open(YoutubePlayerComponent, { closeOnBackdropClick: true, context: { videoId: youtubeId } })
        return;
      }
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

  setReact(id: number | undefined, react: string) {
    if (id && react)
      this._chatService.setReact(id, react).catch(() => this.toastrService.danger('', "יש להתחבר לחשבון בכדי להוסיף אימוג'ים"));
  }

  showEmojiMenu() {
    this.cancelEmojiMenuClose();
    this.popover.open()
  }

  scheduleEmojiMenuClose() {
    this.closeEmojiMenuTimeout = setTimeout(() => {
      this.popover.close();
    }, 150);
  }

  cancelEmojiMenuClose() {
    if (this.closeEmojiMenuTimeout) {
      clearTimeout(this.closeEmojiMenuTimeout);
      this.closeEmojiMenuTimeout = undefined;
    }
  }

  hasReactions(reactions: any): boolean {
    return reactions && Object.keys(reactions).length > 0;
  }

  getPopoverPlacement(): string {
    const element = document.querySelector(`[data-message-id="${this.message?.id}"]`) as HTMLElement;
    if (element) {
      const rect = element.getBoundingClientRect();
      const spaceBelow = window.innerHeight - rect.bottom;
      const spaceAbove = rect.top;

      if (spaceBelow < 150 && spaceAbove > 150) {
        return 'top';
      }
    }

    return 'bottom';
  }

  isEdited(message: ChatMessage): boolean {
    if (!message.last_edit) return false;
    const date = new Date(message.last_edit).getFullYear();
    if (isNaN(date)) return false;
    return date !== 1;
  }
}
