import { Component, Input, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbSelectModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, ChannelUser } from '../../../services/super-admin.service';

@Component({
  selector: 'app-channel-users',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbIconModule,
    NbSelectModule,
  ],
  templateUrl: './channel-users.component.html',
})
export class ChannelUsersComponent implements OnInit {
  @Input() slug!: string;

  users: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  saving = false;
  addingUser = false;
  newEmail = '';
  newRole: 'owner' | 'moderator' | 'writer' | '' = 'moderator';

  roleOptions: { value: string; label: string }[] = [
    { value: 'owner', label: 'בעלים' },
    { value: 'moderator', label: 'מנהל' },
    { value: 'writer', label: 'כותב' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadUsers();
  }

  loadUsers() {
    this.superAdminService.getChannelUsers(this.slug)
      .then(users => {
        this.users = [...users];
        this.removedUsers = [];
      })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת משתמשי הערוץ'));
  }

  addUser() {
    if (!this.newEmail) {
      this.toastr.warning('', 'יש להזין כתובת אימייל');
      return;
    }
    this.users.push({ email: this.newEmail, role: this.newRole as any });
    this.newEmail = '';
    this.newRole = 'moderator';
    this.addingUser = false;
  }

  removeUser(index: number) {
    const [removed] = this.users.splice(index, 1);
    if (removed?.email) this.removedUsers.push({ email: removed.email, role: '' });
  }

  save() {
    this.saving = true;
    this.superAdminService.setChannelUsers(this.slug, [...this.users, ...this.removedUsers])
      .then(() => {
        this.removedUsers = [];
        this.toastr.success('', 'המשתמשים נשמרו בהצלחה');
      })
      .catch(() => this.toastr.danger('', 'שגיאה בשמירת המשתמשים'))
      .finally(() => this.saving = false);
  }

  getRoleLabel(role: string): string {
    const opt = this.roleOptions.find(o => o.value === role);
    return opt ? opt.label : role;
  }
}
