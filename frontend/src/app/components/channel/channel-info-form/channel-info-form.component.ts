import { uploadErrorMessage } from '../../../services/upload-error';
import { Component, OnInit } from '@angular/core';
import {
  NbButtonModule, NbCardModule, NbIconModule, NbInputModule, NbSpinnerModule, NbToastrService, NbTooltipModule,
} from '@nebular/theme';
import { FormsModule } from '@angular/forms';
import { HttpEventType } from '@angular/common/http';
import { Channel } from '../../../models/channel.model';
import { AdminService } from '../../../services/admin.service';
import { ChatService, Attachment, ChatFile } from '../../../services/chat.service';
import { MyChannelsService } from '../../../services/my-channels.service';

/**
 * "פרטי הערוץ": the name, description, logo and contact link readers see in
 * the channel header. Edits are local until "שמירה"; the logo file itself is
 * uploaded the moment it is picked, so the save only ever posts a server URL.
 */
@Component({
  selector: 'app-channel-info-form',
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbSpinnerModule,
    NbInputModule,
    NbIconModule,
    NbTooltipModule,
  ],
  templateUrl: './channel-info-form.component.html',
  styleUrl: './channel-info-form.component.scss'
})
export class ChannelInfoFormComponent implements OnInit {

  readonly nameMax = 80;
  readonly descriptionMax = 2000;

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private myChannels: MyChannelsService,
  ) { }

  attachment?: Attachment;
  channel: Channel = {};
  isSending = false;
  loading = true;
  /** The last copy the server confirmed — what "ביטול שינויים" goes back to. */
  private saved: Channel = {};

  ngOnInit(): void {
    // Opened by deep link the channel info may still be in flight; the dialog
    // this used to be could rely on the feed having loaded it already.
    this.chatService.ensureChannelInfo()
      .catch(() => undefined)
      .finally(() => {
        this.saved = { ...this.chatService.channelInfo };
        this.channel = { ...this.saved };
        this.loading = false;
      });
  }

  get initial(): string {
    return (this.channel.name || '?').trim().charAt(0).toUpperCase();
  }

  get dirty(): boolean {
    const a = this.channel, b = this.saved;
    return (a.name ?? '') !== (b.name ?? '')
      || (a.description ?? '') !== (b.description ?? '')
      || (a.logoUrl ?? '') !== (b.logoUrl ?? '')
      || (a.contact_us ?? '') !== (b.contact_us ?? '');
  }

  get uploading(): boolean {
    return !!this.attachment?.uploading;
  }

  resetChanges(): void {
    if (this.uploading) return;
    this.channel = { ...this.saved };
  }

  editChannelInfo() {
    if (!this.channel.name?.trim()) {
      this.toastrService.warning("", "יש להזין שם לערוץ");
      return;
    }
    // While the logo upload is in flight logoUrl still holds the data: preview,
    // which the server rejects (400 'invalid logo URL') with no hint which
    // field is wrong — wait for the server-issued URL instead of posting it.
    if (this.attachment?.uploading) {
      this.toastrService.warning("", "הלוגו עדיין בהעלאה, המתינו לסיום ונסו שוב");
      return;
    }
    this.isSending = true;
    const name = this.channel.name.trim();
    const description = this.channel.description || '';
    const logoUrl = this.channel.logoUrl || '';
    const contactUs = (this.channel.contact_us || '').trim();
    this.chatService.editChannelInfo(name, description, logoUrl, contactUs).subscribe({
      next: () => {
        this.isSending = false;
        this.toastrService.success("", "פרטי הערוץ נשמרו");
        this.saved = { ...this.channel, name, description, logoUrl, contact_us: contactUs };
        this.channel = { ...this.saved };
        // The header on this page and the channel switcher both show the name
        // and logo; refresh the sources they read from.
        this.chatService.updateChannelInfo().catch(() => undefined);
        this.myChannels.invalidate();
      },
      error: (err) => {
        this.isSending = false;
        // The server refuses a contact link that is not http(s)/mailto — say
        // which field, rather than a generic failure.
        const text = typeof err?.error === 'string' ? err.error : '';
        if (err?.status === 400 && text.includes('contact')) {
          this.toastrService.danger("", "קישור צור קשר חייב להתחיל ב-https://‎ או ב-mailto:");
          return;
        }
        if (err?.status === 400 && text.includes('too long')) {
          this.toastrService.danger("", "השם או התיאור ארוכים מדי (עד 80 תווים לשם ועד 2000 לתיאור)");
          return;
        }
        // 'invalid logo URL': a data: preview or a non-http(s) address reached
        // the save — picking the logo again is the only way out.
        if (err?.status === 400 && text.includes('logo')) {
          this.toastrService.danger("", "כתובת הלוגו אינה תקינה, בחרו את הלוגו מחדש");
          return;
        }
        if (err?.status === 403 || err?.status === 401) {
          this.toastrService.danger("", "אין לכם הרשאה לערוך את פרטי הערוץ");
          return;
        }
        this.toastrService.danger("", "עריכת פרטי ערוץ נכשלה");
      }
    });
  };

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;

    if (input.files && input.files[0]) {
      this.attachment = { file: input.files[0] }
      // Kept so a failed upload can put the previous logo back: the data: URL
      // preview set below is only ever replaced on success, and leaving it in
      // place made every later save of this form fail on 'invalid logo URL'.
      const previousLogoUrl = this.channel.logoUrl;
      const reader = new FileReader();
      reader.readAsDataURL(this.attachment.file);
      reader.onload = (event) => {
        if (event.target) {
          this.channel.logoUrl = event.target.result as string;
        }
      }

      this.uploadFile(this.attachment, previousLogoUrl);
    }
    // Cleared so picking the same logo again (after a failed upload) fires a
    // change event; a browser only fires it when the selection differs.
    input.value = '';
  }

  async uploadFile(attachment: Attachment, previousLogoUrl?: string) {
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
            attachment.uploading = false;
            attachment.uploadProgress = 0;
            if (!uploadedFile) return;
            this.channel.logoUrl = uploadedFile.url;
          }
        },
        error: (error) => {
          this.toastrService.danger("", uploadErrorMessage(error.status));
          attachment.uploading = false;
          // Drop the data: preview — only a server URL may reach the save.
          this.channel.logoUrl = previousLogoUrl;
        },
      });

    } catch (error) {
      this.toastrService.danger("", "שגיאה בהעלאת קובץ");
    }
  }
}
