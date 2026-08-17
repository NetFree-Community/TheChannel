import { Component, ElementRef, EventEmitter, OnDestroy, OnInit, Output, ViewChild } from '@angular/core';

import { HttpEventType } from "@angular/common/http";
import { FormsModule } from "@angular/forms";
import { firstValueFrom, Subscription } from "rxjs";
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbDialogService,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
  NbProgressBarModule,
  NbSpinnerModule,
  NbTagModule,
  NbToastrService,
  NbToggleModule
} from "@nebular/theme";
import { MarkdownComponent } from "ngx-markdown";
import { NgIconsModule } from "@ng-icons/core";
import { Attachment, ChatFile, ChatMessage, ChatService } from '../../../../services/chat.service';
import { AdminService, EditMsg } from '../../../../services/admin.service';
import { AutosizeModule } from "ngx-autosize";
import { TimePickerComponent } from './time-picker/time-picker.component';

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
    NbProgressBarModule,
    NbCardModule,
    NbFormFieldModule,
    NbToggleModule,
    NbSpinnerModule,
    MarkdownComponent,
    NbTagModule,
    NbAlertModule,
    NgIconsModule,
    AutosizeModule
  ],
  templateUrl: './input-form.component.html',
  styleUrl: './input-form.component.scss'
})
export class InputFormComponent implements OnInit, OnDestroy {

  protected readonly maxMessageLength: number = 2048;

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

  @Output() inputHeightChanged = new EventEmitter<number>();

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private dialogService: NbDialogService,
    protected chatService: ChatService,
  ) { }

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
        this.schedulingMessage = edit.message?.timestamp;
      }
      if (edit.new) {
        this.input = this.input ? `${this.input}\n${edit.message.text}` : edit.message.text || '';
      } else {
        this.message = edit.message;
        this.input = this.message?.text || '';
        this.isAds = this.message?.is_ads || false;
      }
    });
  }

  ngOnDestroy(): void {
    this.subscription.unsubscribe();
  }

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files) {
      let newAttachment: Attachment = { file: input.files[0] };
      let i = this.attachments.push(newAttachment) - 1;

      let reader = new FileReader();
      reader.readAsDataURL(newAttachment.file);
      reader.onload = (event) => {
        if (event.target) {
          this.attachments[i].url = event.target.result as string;
        }
      }

      this.uploadFile(this.attachments[i]);
    }
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
              embedded = `[image-embedded#](${uploadedFile.url})`; //`![${uploadedFile.filename}](${uploadedFile.url})`;

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
          if (error.status === 413) {
            this.toastrService.danger("", "קובץ גדול מדי");
          } else {
            this.toastrService.danger("", "שגיאה בהעלאת קובץ");
          }
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

      let result: boolean;
      result = this.schedulingMessage ? await this.saveSchedulingMessage() : this.message ? await this.updateMessage() : await this.sendNewMessage();

      if (!result) {
        throw new Error();
      }

      this.toastrService.success("", "הודעה פורסמה בהצלחה");
      this.clearInputs();
    } catch (error) {
      this.toastrService.danger("", "שגיאה בפרסום הודעה");
    } finally {
      this.isSending = false
    }
  }

  async updateMessage(): Promise<boolean> {
    if (!this.message) return false;
    this.message.text = this.input;
    this.message.deleted = false;
    this.message.is_ads = this.isAds;
    // A rejected save must leave the composer untouched — reporting success here
    // would clear the textarea and lose whatever the user just wrote.
    const res = await firstValueFrom(this.adminService.editMessage(this.message));
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
        this.message.text = this.input;
        this.message.is_ads = this.isAds;
        this.message.timestamp = this.schedulingMessage;

        await this.adminService.editScheduledMessage(this.message);
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

  openMarkdownDocs() {
    let markdownDocsUrl = 'https://www.markdownguide.org/basic-syntax/';
    window.open(markdownDocsUrl, '_blank');
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
    this.dialogService.open(TimePickerComponent, {
      context: {
        date: this.schedulingMessage,
      }
    }).onClose.subscribe((date: Date | undefined) => {
      this.schedulingMessage = date;
    });
  }
}
