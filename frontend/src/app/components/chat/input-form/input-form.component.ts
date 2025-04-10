import { Component, ElementRef, OnInit, ViewChild } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ChatMessage, ChatService, ChatFile , Attachment} from "../../../services/chat.service";
import { HttpClient, HttpEventType } from "@angular/common/http";
import { FormsModule } from "@angular/forms";
import { firstValueFrom } from "rxjs";
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule, NbDialogRef,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
  NbProgressBarModule, NbSpinnerModule, NbTagModule, NbToastrService, NbToggleModule
} from "@nebular/theme";
import { AngularEditorModule } from "@kolkov/angular-editor"; // Corrected import path
import { MarkdownComponent } from "ngx-markdown";
import { NgIconsModule } from "@ng-icons/core";
import { heroBold, heroItalic, heroUnderline, heroCodeBracket, heroPaperClip, heroQuestionMarkCircle } from "@ng-icons/heroicons/outline";
import { MarkdownHelpComponent } from "../markdown-help/markdown-help.component";

@Component({
  selector: 'app-input-form',
  imports: [
    CommonModule,
    FormsModule,
    NbInputModule,
    NbIconModule,
    NbButtonModule,
    NbProgressBarModule,
    NbCardModule,
    NbFormFieldModule,
    AngularEditorModule, // Corrected module name
    NbToggleModule,
    NbSpinnerModule,
    MarkdownComponent,
    NbTagModule,
    NbAlertModule,
    NgIconsModule, // Use NgIconsModule directly, icons are configured in app.config.ts
    MarkdownHelpComponent,
  ],
  templateUrl: './input-form.component.html',
  styleUrl: './input-form.component.scss'
})
export class InputFormComponent implements OnInit {

  protected readonly maxMessageLength: number = 2048;

  message?: ChatMessage;

  attachments: Attachment[] = [];

  input: string = '';
  isSending: boolean = false;
  showMarkdownPreview: boolean = false;
  showMarkdownHelp: boolean = false;
  hasScrollbar: boolean = false;

  @ViewChild('inputTextArea') inputTextArea!: ElementRef<HTMLTextAreaElement>;

  constructor(private http: HttpClient, private chatService: ChatService, private toastrService: NbToastrService, protected dialogRef: NbDialogRef<InputFormComponent>) { }

  ngOnInit() {
    if (this.message) {
      this.input = this.message.text || '';
    }
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

      this.chatService.uploadFile(formData).subscribe({
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

      let result = this.message ? await this.updateMessage() : await this.sendNewMessage();
      if (!result) {
        throw new Error();
      }

      this.toastrService.success("", "הודעה פורסמה בהצלחה");
      this.dialogRef.close(this.message);
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
    await firstValueFrom(this.chatService.editMessage(this.message));
    return true;
  }

  async sendNewMessage(): Promise<boolean> {
    if (!this.input.trim() && !this.attachments.length) return false;

    let newMessage: ChatMessage = {
      type: 'md',
      text: this.input,
      file: undefined,
    };

    this.message = await firstValueFrom(this.chatService.addMessage(newMessage));

    if (!this.message) {
      throw new Error();
    }

    return true;
  }

  closeDialog() {
    this.dialogRef.close();
  }

  removeAttachment(attachment: Attachment) {
    this.attachments = this.attachments.filter((file) => file !== attachment);
    this.input = this.input.replaceAll(attachment.embedded ?? '', '');
  }

  toggleMarkdownHelp() {
    this.showMarkdownHelp = !this.showMarkdownHelp;
  }

  checkScrollbar() {
    if (this.inputTextArea?.nativeElement) {
      const textarea = this.inputTextArea.nativeElement;
      this.hasScrollbar = textarea.scrollHeight > textarea.clientHeight;
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

  ngAfterViewInit() {
    this.checkScrollbar();
    
    if (this.inputTextArea?.nativeElement) {
      this.inputTextArea.nativeElement.addEventListener('input', () => {
        setTimeout(() => this.checkScrollbar(), 0);
      });
      
      window.addEventListener('resize', () => {
        setTimeout(() => this.checkScrollbar(), 0);
      });
    }
  }
  
  ngOnDestroy() {
    if (this.inputTextArea?.nativeElement) {
      this.inputTextArea.nativeElement.removeEventListener('input', this.checkScrollbar);
      window.removeEventListener('resize', this.checkScrollbar);
    }
  }
}
