import { Component, OnInit } from '@angular/core';
import { Channel } from '../../../models/channel.model';
import { NbCardModule, NbDialogRef, NbButtonModule, NbSpinnerModule, NbInputModule, NbToastrService, NbPopoverModule } from '@nebular/theme';
import { Attachment, ChatFile, ChatService } from '../../../services/chat.service';
import { FormsModule } from '@angular/forms';
import { HttpEventType } from '@angular/common/http';

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
    protected dialogRef: NbDialogRef<ChannelInfoFormComponent>,
    private chatService: ChatService,
    private taostrService: NbToastrService,
    private toastrService: NbToastrService,
  ) { }

  ngOnInit(): void {
    this.channel = this.dialogRef.componentRef.instance.channel;
    this.name = this.channel.name;
    this.description = this.channel.description
    this.logoUrl = this.channel.logoUrl;
  }

  attachment!: Attachment;
  channel!: Channel;
  isSending: boolean = false;
  name!: string;
  description!: string;
  logoUrl!: string;

  editChannelInfo() {
    this.isSending = true;
    this.chatService.editChannelInfo(this.name, this.description, this.logoUrl).subscribe({
      next: () => {
        console.log("logURL", this.logoUrl);
        this.channel.name = this.name;
        this.channel.description = this.description;
        this.channel.logoUrl = this.logoUrl;
        this.isSending = false;
        this.taostrService.success("", "עריכת פרטי ערוץ בוצעה בהצלחה");
        this.dialogRef.close();
      },
      error: () => {
        this.isSending = false;
        this.taostrService.danger("", "עריכת פרטי ערוץ נכשלה");
      }
    });
  };

  onFileSelected(event: Event) {
    const input = event.target as HTMLInputElement;

    if (input.files) {
      console.log(input.files);
      this.attachment = { file: input.files[0] }
      const reader = new FileReader();
      reader.readAsDataURL(this.attachment.file);
      reader.onload = (event) => {
        if (event.target) {
          this.logoUrl = event.target.result as string;
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

      this.chatService.uploadFile(formData).subscribe({
        next: (event) => {
          if (event.type === HttpEventType.UploadProgress) {
            attachment.uploadProgress = Math.round((event.loaded / (event.total || 1)) * 100);
          } else if (event.type === HttpEventType.Response) {
            const uploadedFile: ChatFile | null = event.body || null;
            attachment.uploading = false;
            attachment.uploadProgress = 0;
            if (!uploadedFile) return;
            console.log("uploadedFile", uploadedFile);
            this.logoUrl = uploadedFile.url;
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

  closeDialog() {
    this.dialogRef.close();
  };
}