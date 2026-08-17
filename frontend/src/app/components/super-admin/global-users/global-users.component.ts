import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, SuperAdminUser } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-users',
  standalone: true,
  imports: [
    CommonModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
  ],
  templateUrl: './global-users.component.html',
})
export class GlobalUsersComponent implements OnInit {
  users: SuperAdminUser[] = [];
  loading = true;

  globalRoleLabels: Record<string, string> = {
    super_admin: 'מנהל-על',
    admin: 'מנהל',
    user: 'משתמש',
    '': 'רגיל',
  };

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.superAdminService.getGlobalUsers()
      .then(users => this.users = users)
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת רשימת משתמשים'))
      .finally(() => this.loading = false);
  }

  getChannelRolesDisplay(channelRoles: Record<string, string>): string[] {
    return Object.entries(channelRoles || {}).map(([slug, role]) => `${slug}: ${role}`);
  }

  getRoleLabel(role: string): string {
    return this.globalRoleLabels[role] || role;
  }
}
