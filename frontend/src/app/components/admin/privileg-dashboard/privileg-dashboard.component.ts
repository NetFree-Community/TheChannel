import { Component, OnInit } from '@angular/core';
import { AdminService, ChannelUser } from '../../../services/admin.service';
import { NbButtonModule, NbCardModule, NbInputModule, NbToastrService, NbIconModule, NbSelectModule } from "@nebular/theme";
import { FormsModule } from '@angular/forms';
import { AuthService } from '../../../services/auth.service';
import { SlugService } from '../../../services/slug.service';

@Component({
  selector: 'app-privileg-dashboard',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    FormsModule,
    NbIconModule,
    NbSelectModule,
  ],
  templateUrl: './privileg-dashboard.component.html',
  styleUrl: './privileg-dashboard.component.scss'
})
export class PrivilegDashboardComponent implements OnInit {

  constructor(
    private adminService: AdminService,
    private toastService: NbToastrService,
    public authService: AuthService,
    private slugService: SlugService,
  ) { }

  usersList: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  addingNewUser = false;
  newUserEmail = '';
  newUserRole: 'moderator' | 'writer' = 'writer';

  readonly roleOptions = [
    { value: 'moderator', label: 'מנהל' },
    { value: 'writer', label: 'כותב' },
  ];

  get isOwner(): boolean {
    const roles = this.authService.userInfo?.channelRoles;
    if (this.authService.userInfo?.globalRole === 'super_admin') return true;
    return roles?.[this.slugService.slug] === 'owner';
  }

  ngOnInit(): void {
    this.adminService.getChannelUsers()
      .then(list => {
        this.usersList = list;
        this.removedUsers = [];
      })
      .catch(() => this.toastService.danger('', 'שגיאה בטעינת המשתמשים'));
  }

  saveChanges() {
    this.adminService.setChannelUsers([...this.usersList, ...this.removedUsers])
      .then(() => {
        this.removedUsers = [];
        this.toastService.success('', 'השינויים נשמרו בהצלחה!');
      })
      .catch(() => this.toastService.danger('', 'שגיאה בשמירת השינויים'));
  }

  deleteUser(index: number) {
    if (!confirm('האם אתה בטוח שברצונך למחוק את המשתמש הזה?')) return;
    const [removed] = this.usersList.splice(index, 1);
    if (removed?.email) this.removedUsers.push({ email: removed.email, role: '' });
  }

  saveNewUser() {
    if (!this.newUserEmail) return;
    this.usersList.push({ email: this.newUserEmail, role: this.newUserRole });
    this.newUserEmail = '';
    this.newUserRole = 'writer';
    this.addingNewUser = false;
  }

  resetNewUser() {
    this.newUserEmail = '';
    this.newUserRole = 'writer';
    this.addingNewUser = false;
  }

  getRoleLabel(role: string): string {
    return this.roleOptions.find(o => o.value === role)?.label ?? role;
  }
}
