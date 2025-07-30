import { Component, OnInit } from '@angular/core';
import { ChatService } from '../../../services/chat.service';
import { NbToastrService, NbCardModule, NbButtonModule, NbIconModule, NbListModule } from '@nebular/theme';
import { AdminService } from '../../../services/admin.service';
import { PickerModule } from '@ctrl/ngx-emoji-mart';

@Component({
  selector: 'app-emojis',
  imports: [
    NbCardModule,
    NbButtonModule,
    PickerModule,
    NbIconModule,
    NbListModule
],
  templateUrl: './emojis.component.html',
  styleUrl: './emojis.component.scss'
})
export class EmojisComponent implements OnInit {
  emojis: string[] | undefined = [];

  constructor(
    private chatService: ChatService,
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit(): void {
    this.chatService.getEmojisList(true)
      .then(emojis => this.emojis = emojis)
      .catch(() => {
        this.toastrService.danger('', 'שגיאה בהגדרת אימוגים');
        this.emojis = undefined;
      });
  }

  setEmojis() {
    // if (!this.emojis?.length) {
    //   this.toastrService.warning('', 'אין אימוגים להגדיר');
    //   return;
    // }

    this.adminService.setEmojis(this.emojis)
      .then(() => {
        this.toastrService.success('', 'אימוגים הוגדרו בהצלחה');
      })
      .catch(() => {
        this.toastrService.danger('', 'שגיאה בהגדרת אימוגים');
      });
  }

  addEmoji(event: any) {
    const emoji = event.emoji.native;
    if (!this.emojis?.includes(emoji)) this.emojis?.push(emoji)
  }

  removeEmoji(emoji: number) {
    this.emojis?.splice(emoji, 1);
  }
}
