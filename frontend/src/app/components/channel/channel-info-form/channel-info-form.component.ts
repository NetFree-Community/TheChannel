import { Component, OnInit } from '@angular/core';
import { NbCardModule, NbDialogRef, NbButtonModule, NbSpinnerModule, NbInputModule, NbToastrService, NbPopoverModule } from '@nebular/theme';
import { FormsModule } from '@angular/forms';
import { HttpEventType } from '@angular/common/http';
import { Channel } from '../../../models/channel.model';
import { AdminService } from '../../../services/admin.service';
import { ChatService, Attachment, ChatFile } from '../../../services/chat.service';


@Component({
  selector: 'app-channel-info-form',
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbSpinnerModule,
    NbInputModule,
    NbPopoverModule,
  ],
  templateUrl: './channel-info-form.component.html',
  styleUrl: './channel-info-form.component.scss'
})
export class ChannelInfoFormComponent implements OnInit {

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.channel = { ...this.chatService.channelInfo };
  }

  attachment!: Attachment;
  channel: Channel = {};
  isSending: boolean = false;

  editChannelInfo() {
    this.isSending = true;
    this.chatService.editChannelInfo(this.channel.name || '', this.channel.description || '', this.channel.logoUrl || '').subscribe({
      next: () => {
        this.isSending = false;
        this.toastrService.success("", "עריכת פרטי ערוץ בוצעה בהצלחה");
        this.chatService.updateChannelInfo();
      },
      error: () => {
        this.isSending = false;
        this.toastrService.danger("", "עריכת פרטי ערוץ נכשלה");
      }
    });
  };

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;

    if (input.files) {
      this.attachment = { file: input.files[0] }
      const reader = new FileReader();
      reader.readAsDataURL(this.attachment.file);
      reader.onload = (event) => {
        if (event.target) {
          this.channel.logoUrl = event.target.result as string;
        }
      }

      this.uploadFile(this.attachment);
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
            attachment.uploading = false;
            attachment.uploadProgress = 0;
            if (!uploadedFile) return;
            this.channel.logoUrl = uploadedFile.url;
          }
        },
        error: (error) => {
          if (error.status === 413) {
            this.toastrService.danger("", "קובץ גדול מדי");
          } else {
            this.toastrService.danger("", "שגיאה בהעלאת קובץ");
          }
          attachment.uploading = false;
        },
      });

    } catch (error) {
      this.toastrService.danger("", "שגיאה בהעלאת קובץ");
    }
  }
}