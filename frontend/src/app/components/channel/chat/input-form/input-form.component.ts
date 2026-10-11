import { uploadErrorMessage } from '../../../../services/upload-error';
import { AfterViewInit, Component, ElementRef, EventEmitter, OnDestroy, OnInit, Output, ViewChild } from '@angular/core';

import { HttpEventType } from "@angular/common/http";
import { FormsModule } from "@angular/forms";
import { firstValueFrom, Subscription } from "rxjs";
import {
  NbAlertModule,
  NbButtonModule,
  NbDialogService,
  NbIconModule,
  NbInputModule,
  NbPopoverDirective,
  NbPopoverModule,
  NbToastrService,
  NbToggleModule,
  NbTooltipModule
} from "@nebular/theme";
import { MarkdownComponent } from "ngx-markdown";
import { NgIconsModule } from "@ng-icons/core";
import { Attachment, ChatFile, ChatMessage, ChatService } from '../../../../services/chat.service';
import { AdminService, EditMsg } from '../../../../services/admin.service';
import { AutosizeModule } from "ngx-autosize";
import { TimePickerComponent } from './time-picker/time-picker.component';
import { MessageTimePipe } from '../../../../pipes/message-time.pipe';

// Per-user client-side preference: the server never consumed this setting, and
// the channel settings endpoint it used to arrive through is owner-only, so a
// writer or moderator could never receive it.
const ENTER_SENDS_MESSAGE_KEY = 'enterSendsMessage';

@Component({
  selector: 'app-input-form',
  imports: [
    FormsModule,
    NbInputModule,
    NbIconModule,
    NbButtonModule,
    NbToggleModule,
    MarkdownComponent,
    NbAlertModule,
    NgIconsModule,
    NbTooltipModule,
    NbPopoverModule,
    AutosizeModule,
    MessageTimePipe,
  ],
  templateUrl: './input-form.component.html',
  styleUrl: './input-form.component.scss'
})
export class InputFormComponent implements OnInit, AfterViewInit, OnDestroy {

  protected readonly maxMessageLength: number = 2048;
  /** The counter appears from here on, so a writer sees the limit before hitting it. */
  protected readonly counterFrom: number = 1800;
  // The first line of a reply is the quote token the message component builds.
  private readonly quoteLine = /^\[quote-embedded#\]\([^\n]*\)[ \t]*\n?/;

  message?: ChatMessage;

  attachments: Attachment[] = [];

  input: string = '';
  isAds: boolean = false;
  schedulingMessage: Date | undefined = undefined;
  isSending: boolean = false;
  showMarkdownPreview: boolean = false;
  hasScrollbar: boolean = false;
  enterSendsMessage: boolean = false;
  private subscription!: Subscription;

  @ViewChild('inputTextArea') inputTextArea!: ElementRef<HTMLTextAreaElement>;
  @ViewChild('helpPop') private helpPop?: NbPopoverDirective;

  @Output() inputHeightChanged = new EventEmitter<number>();

  // Anything that changes the composer's height — the edit banner, an
  // attachment chip, the preview, a toolbar that wraps on resize — has to reach
  // the shell, which places the feed above the composer by this height. The
  // textarea's own resize event only covered the textarea.
  private sizeObserver?: ResizeObserver;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private dialogService: NbDialogService,
    protected chatService: ChatService,
    private host: ElementRef<HTMLElement>,
  ) { }

  ngAfterViewInit(): void {
    if (typeof ResizeObserver === 'undefined') return;
    this.sizeObserver = new ResizeObserver(() => {
      this.inputHeightChanged.emit(this.host.nativeElement.offsetHeight);
    });
    this.sizeObserver.observe(this.host.nativeElement);
  }

  /** Whether the message being composed starts with a quote of another message. */
  get hasQuote(): boolean {
    return this.quoteLine.test(this.input);
  }

  /** Drops the quote line, keeping whatever the writer typed under it. */
  removeQuote() {
    this.input = this.input.replace(this.quoteLine, '');
    this.inputTextArea?.nativeElement.focus();
  }

  /**
   * The send button is live once there is something to send. Files still
   * uploading do not disable it: sendMessage() explains that case itself.
   */
  get canSend(): boolean {
    if (this.isSending) return false;
    return !!this.input.trim() || this.attachments.length > 0;
  }

  ngOnInit() {
    this.enterSendsMessage = localStorage.getItem(ENTER_SENDS_MESSAGE_KEY) === '1';

    if (this.message) {
      this.input = this.message.text || '';
    }

    this.subscription = this.adminService.messageEditObservable.subscribe((edit?: EditMsg) => {
      if (!edit) {
        // Cleared (e.g. on a channel switch) — drop every leftover of the
        // previous compose/edit so nothing targets the wrong channel.
        this.message = undefined;
        this.input = '';
        this.isAds = false;
        this.attachments = [];
        this.schedulingMessage = undefined;
        return;
      }
      if (edit.isScheduling) {
        // The feed's entry carries the timestamp as the wire string; the time
        // picker needs a real Date.
        this.schedulingMessage = edit.message?.timestamp ? new Date(edit.message.timestamp as unknown as string) : undefined;
      } else if (!edit.new) {
        // Editing a LIVE message: a schedule time picked earlier must not
        // survive, or send takes the scheduling branch with a live message
        // id and writes it into the scheduled list at that index. (A quote —
        // edit.new — only adds text, so a chosen time is kept there.)
        this.schedulingMessage = undefined;
      }
      if (edit.new) {
        this.input = this.input ? `${this.input}\n${edit.message.text}` : edit.message.text || '';
      } else if (edit.prepend && this.message === edit.message) {
        // A quote added to the edit already open here: goes in front of the
        // text as typed so far, the target object stays as it is.
        this.input = edit.prepend + this.input;
      } else {
        this.message = edit.message;
        this.input = (edit.prepend || '') + (this.message?.text || '');
        this.isAds = this.message?.is_ads || false;
      }
    });
  }

  ngOnDestroy(): void {
    this.subscription.unsubscribe();
    this.sizeObserver?.disconnect();
  }

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files) {
      // The picker allows several files; every one of them is attached.
      // Only the first used to be, and the rest vanished without a word.
      for (const file of Array.from(input.files)) {
        const newAttachment: Attachment = { file };
        const i = this.attachments.push(newAttachment) - 1;

        const reader = new FileReader();
        reader.readAsDataURL(file);
        reader.onload = (event) => {
          if (event.target) {
            this.attachments[i].url = event.target.result as string;
          }
        }

        this.uploadFile(this.attachments[i]);
      }
    }
    // Cleared so picking the same file again (after removing it) fires a
    // change event; a browser only fires it when the selection differs.
    input.value = '';
  }

  async uploadFile(attachment: Attachment) {
    try {
      const formData = new FormData();
      if (!attachment.file) return;
      formData.append('file', attachment.file);

      attachment.uploading = true;

      this.adminService.uploadFile(formData).subscribe({
        next: (event) => {
          if (event.type === HttpEventType.UploadProgress) {
            attachment.uploadProgress = Math.round((event.loaded / (event.total || 1)) * 100);
          } else if (event.type === HttpEventType.Response) {
            const uploadedFile: ChatFile | null = event.body || null;
            let embedded = '';

            if (!uploadedFile) return;
            if (uploadedFile?.filetype === 'image') {
              // The size, when the server could read it, rides in the token so
              // the renderer can reserve the picture's box before it loads.
              // Omitted when unknown, which is what every older message looks
              // like and renders exactly as it always did.
              const size = uploadedFile.width && uploadedFile.height
                ? `${uploadedFile.width}x${uploadedFile.height}`
                : '';
              embedded = `[image-embedded#${size}](${uploadedFile.url})`;

            } else if (uploadedFile?.filetype === 'video') {
              embedded = `[video-embedded#](${uploadedFile.url})`;

            } else if (uploadedFile?.filetype === 'audio') {
              embedded = `[audio-embedded#](${uploadedFile.url})`;

            } else {
              embedded = `[${uploadedFile.filename}](${uploadedFile.url})`;
            }
            this.input += (this.input ? '\n' : '') + embedded;
            attachment.embedded = embedded;
            attachment.uploading = false;
          }
        },
        error: (error) => {
          this.toastrService.danger("", uploadErrorMessage(error.status));
          attachment.uploading = false;
          this.removeAttachment(attachment);
        }
      });
    } catch (error) {
      this.toastrService.danger("", "שגיאה בהעלאת קובץ");
    }
  }

  async sendMessage() {
    try {
      this.isSending = true;

      const hasPendingFiles = this.attachments.some((attachment) => attachment.uploading);
      if (hasPendingFiles) {
        this.toastrService.danger("", "יש קבצים בהעלאה");
        this.isSending = false;
        return;
      }

      if (!this.input.trim() && !this.attachments.length) {
        this.toastrService.danger("", "לא ניתן לפרסם הודעה ריקה");
        this.isSending = false;
        return;
      }

      const scheduled = !!this.schedulingMessage;
      let result: boolean;
      result = scheduled ? await this.saveSchedulingMessage() : this.message ? await this.updateMessage() : await this.sendNewMessage();

      if (!result) {
        throw new Error();
      }

      this.toastrService.success("", scheduled ? "ההודעה תוזמנה בהצלחה" : "הודעה פורסמה בהצלחה");
      this.clearInputs();
    } catch (error) {
      this.toastrService.danger("", "שגיאה בפרסום הודעה");
    } finally {
      this.isSending = false
    }
  }

  async updateMessage(): Promise<boolean> {
    if (!this.message) return false;
    // Posted as a copy: this.message IS the feed's object, and writing the edit
    // into it before the request meant a rejected save (403/500) still showed
    // the unsaved text and an un-deleted state until reload. The feed is
    // updated by the 'edit-message' SSE event, which carries the saved message.
    //
    // A rejected save must leave the composer untouched — reporting success here
    // would clear the textarea and lose whatever the user just wrote.
    const res = await firstValueFrom(this.adminService.editMessage({
      ...this.message, text: this.input, deleted: false, is_ads: this.isAds,
    }));
    if (!res?.success) return false;
    this.cancelUpdateMessage();
    return true;
  }

  cancelUpdateMessage() {
    this.adminService.setEditMessage(undefined);
    this.clearInputs();
  }

  async saveSchedulingMessage(): Promise<boolean> {
    let m: ChatMessage = {
      type: 'md',
      text: this.input,
      file: undefined,
      is_ads: this.isAds,
      timestamp: this.schedulingMessage || undefined,
    };

    try {
      if (this.message) {
        // A copy, for the same reason as updateMessage: this.message is the
        // scheduled list's own entry, and the service only commits the new
        // list once the server accepted it. The entry itself goes along too,
        // so the service can find it in the server's current list.
        await this.adminService.editScheduledMessage(this.message, {
          ...this.message, text: this.input, is_ads: this.isAds, timestamp: this.schedulingMessage,
        });
      } else {
        await this.adminService.setScheduledMessage(m);
      }

      this.adminService.reloadSchedulingMessage();
      return true;
    } catch {
      return false;
    }
  }

  async sendNewMessage(): Promise<boolean> {
    let newMessage: ChatMessage = {
      type: 'md',
      text: this.input,
      file: undefined,
      is_ads: this.isAds,
    };

    this.message = await firstValueFrom(this.adminService.addMessage(newMessage));

    if (!this.message) {
      throw new Error();
    }

    return true;
  }

  clearInputs() {
    this.input = '';
    // Sending from preview mode must not leave an empty, read-only preview
    // behind (the toggle is disabled while the input is empty).
    this.showMarkdownPreview = false;
    this.attachments = [];
    this.message = undefined;
    this.isAds = false;
    this.schedulingMessage = undefined;
    this.adminService.setEditMessage(undefined);
  }

  removeAttachment(attachment: Attachment) {
    this.attachments = this.attachments.filter((file) => file !== attachment);
    this.input = this.input.replaceAll(attachment.embedded ?? '', '');
  }

  /** The formatting-help popover is open (Nebular reports both states). */
  helpShown = false;

  onHelpState(shown: boolean) {
    this.helpShown = shown;
    // Focus moves into the card: Escape then closes it from there, and one Tab
    // reaches its only control, the link to the full guide. The popover lives
    // in the overlay container, outside this component's DOM.
    if (shown) setTimeout(() => document.querySelector<HTMLElement>('.cdk-overlay-container .help')?.focus());
  }

  onHelpKeydown(event: KeyboardEvent) {
    const card = event.currentTarget as HTMLElement;
    const more = card.querySelector<HTMLElement>('.help__more');
    const leaving = event.key === 'Tab' && (event.shiftKey ? document.activeElement === card : document.activeElement === more);
    if (event.key !== 'Escape' && !leaving) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
    }
    // Tab is left to the browser: with focus back on the help button, it moves
    // on to the neighbouring toolbar button instead of the end of the document.
    this.closeHelp();
  }

  private closeHelp() {
    this.helpPop?.hide();
    this.host.nativeElement.querySelector<HTMLElement>('.tb--help')?.focus();
  }

  openMarkdownDocs() {
    let markdownDocsUrl = 'https://www.markdownguide.org/basic-syntax/';
    window.open(markdownDocsUrl, '_blank', 'noopener,noreferrer');
  }

  checkScrollbar() {
    if (this.inputTextArea?.nativeElement) {
      const textarea = this.inputTextArea.nativeElement;
      this.hasScrollbar = textarea.scrollHeight > textarea.clientHeight;
    }
  }

  setEnterSendsMessage(checked: boolean) {
    this.enterSendsMessage = checked;
    localStorage.setItem(ENTER_SENDS_MESSAGE_KEY, checked ? '1' : '0');
  }

  onKeydown(event: KeyboardEvent) {
    if (!this.enterSendsMessage) return;
    if (event.key !== 'Enter') return;

    if (event.ctrlKey || event.metaKey) {
      // Ctrl+Enter → insert newline at cursor
      event.preventDefault();
      const ta = this.inputTextArea.nativeElement;
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      this.input = this.input.substring(0, start) + '\n' + this.input.substring(end);
      setTimeout(() => { ta.selectionStart = ta.selectionEnd = start + 1; });
    } else if (!event.shiftKey) {
      // Enter alone → send
      event.preventDefault();
      this.sendMessage();
    }
  }

  onPaste(event: ClipboardEvent) {
    const items = event.clipboardData?.items;
    if (!items) return;
    for (let i = 0; i < items.length; i++) {
      if (items[i].type.startsWith('image/')) {
        // The attach button is hidden when the channel has uploads off; a
        // pasted picture bypassed that and earned a 403 from the server. The
        // paste is left alone so whatever text the clipboard also carries
        // still lands in the textarea.
        if (!this.chatService.fileUploadsEnabled) continue;
        const file = items[i].getAsFile();
        if (!file) continue;
        event.preventDefault();
        const attachment: Attachment = { file };
        const idx = this.attachments.push(attachment) - 1;
        const reader = new FileReader();
        reader.readAsDataURL(file);
        reader.onload = (e) => {
          if (e.target) this.attachments[idx].url = e.target.result as string;
        };
        this.uploadFile(this.attachments[idx]);
      }
    }
  }

  applyFormat(format: 'bold' | 'italic' | 'underline' | 'code') {
    const textArea = this.inputTextArea.nativeElement;
    const start = textArea.selectionStart;
    const end = textArea.selectionEnd;
    const selectedText = this.input.substring(start, end);

    let prefix = '';
    let suffix = '';
    let placeholder = '';

    switch (format) {
      case 'bold':
        prefix = '**';
        suffix = '**';
        placeholder = 'טקסט מודגש';
        break;
      case 'italic':
        prefix = '*';
        suffix = '*';
        placeholder = 'טקסט נטוי';
        break;
      case 'underline':
        prefix = '<u>';
        suffix = '</u>';
        placeholder = 'טקסט עם קו תחתון';
        break;
      case 'code':
        prefix = '```\n';
        suffix = '\n```';
        placeholder = 'קוד';
        // Add new lines if not already present around the selection/cursor
        const before = this.input.substring(0, start);
        const after = this.input.substring(end);
        if (start > 0 && before.charAt(start - 1) !== '\n') {
          prefix = '\n' + prefix;
        }
        if (end < this.input.length && after.charAt(0) !== '\n') {
          suffix = suffix + '\n';
        }
        break;
    }

    let newText = '';
    let cursorPos = start + prefix.length;

    if (selectedText) {
      newText = prefix + selectedText + suffix;
      this.input = this.input.substring(0, start) + newText + this.input.substring(end);
      // Keep the original selection highlighted
      setTimeout(() => {
        textArea.selectionStart = start;
        textArea.selectionEnd = start + newText.length;
        textArea.focus();
      });
    } else {
      newText = prefix + placeholder + suffix;
      this.input = this.input.substring(0, start) + newText + this.input.substring(end);
      // Set cursor position inside the markers or after for code block
      setTimeout(() => {
        if (format === 'code') {
          cursorPos = start + prefix.length; // Cursor at the beginning of the placeholder inside code block
        } else {
          cursorPos = start + prefix.length; // Cursor at the beginning of the placeholder
        }
        textArea.selectionStart = cursorPos;
        textArea.selectionEnd = cursorPos + placeholder.length;
        textArea.focus();
      });
    }
  }

  openTimePicker() {
    // A live message cannot become a scheduled one: the scheduled list is
    // index-based and the save path would refuse the live id with a generic
    // error, so say why instead.
    if (this.message?.id != undefined && !this.schedulingMessage) {
      this.toastrService.warning('', 'לא ניתן לתזמן הודעה שכבר פורסמה');
      return;
    }
    this.dialogService.open(TimePickerComponent, {
      context: {
        date: this.schedulingMessage,
      }
    }).onClose.subscribe((date: Date | undefined) => {
      // ביטול / backdrop-click close with undefined — keep the existing
      // scheduling state instead of silently converting the message to a live one.
      if (date !== undefined) this.schedulingMessage = date;
    });
  }
}
