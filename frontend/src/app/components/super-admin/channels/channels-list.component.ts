import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelData } from '../../../services/super-admin.service';

@Component({
  selector: 'app-channels-list',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
  ],
  templateUrl: './channels-list.component.html',
})
export class ChannelsListComponent implements OnInit {
  @Output() editFeatures = new EventEmitter<string>();
  @Output() manageUsers = new EventEmitter<string>();
  @Output() manageStorage = new EventEmitter<string>();

  channels: ChannelData[] = [];
  showCreateForm = false;
  newSlug = '';
  newName = '';
  newOwnerEmail = '';
  creating = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadChannels();
  }

  loadChannels() {
    this.superAdminService.getChannels()
      .then(channels => this.channels = channels)
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת ערוצים'));
  }

  createChannel() {
    if (!this.newSlug || !this.newName || !this.newOwnerEmail) {
      this.toastr.warning('', 'יש למלא את כל השדות');
      return;
    }
    this.creating = true;
    this.superAdminService.createChannel(this.newSlug, this.newName, this.newOwnerEmail)
      .then(channel => {
        this.channels.push(channel);
        this.showCreateForm = false;
        this.newSlug = '';
        this.newName = '';
        this.newOwnerEmail = '';
        this.toastr.success('', 'הערוץ נוצר בהצלחה');
      })
      .catch((err) => this.toastr.danger(err?.error || '', 'שגיאה ביצירת הערוץ'))
      .finally(() => this.creating = false);
  }

  deleteChannel(slug: string) {
    if (!confirm(`האם אתה בטוח שברצונך למחוק את הערוץ "${slug}"?`)) return;
    this.superAdminService.deleteChannel(slug)
      .then(() => {
        this.channels = this.channels.filter(c => c.slug !== slug);
        this.toastr.success('', 'הערוץ נמחק בהצלחה');
      })
      .catch(() => this.toastr.danger('', 'שגיאה במחיקת הערוץ'));
  }

  cancelCreate() {
    this.showCreateForm = false;
    this.newSlug = '';
    this.newName = '';
    this.newOwnerEmail = '';
  }
}
