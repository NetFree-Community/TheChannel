import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbButtonModule,
  NbCardModule,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
  NbToastrService,
} from '@nebular/theme';
import { SuperAdminService, SuperAdminUser } from '../../../services/super-admin.service';
import { ROLE_LABELS } from '../../../models/my-channel.model';

interface ChannelRoleChip {
  slug: string;
  label: string;
}

@Component({
  selector: 'app-global-users',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbFormFieldModule,
  ],
  templateUrl: './global-users.component.html',
  styleUrl: './global-users.component.scss',
})
export class GlobalUsersComponent implements OnInit {
  users: SuperAdminUser[] = [];
  loading = true;
  loadFailed = false;
  query = '';

  globalRoleLabels: Record<string, string> = {
    super_admin: 'מנהל מערכת',
    admin: 'מנהל',
    user: 'משתמש',
    '': 'משתמש',
  };

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getGlobalUsers()
      .then(users => this.users = users || [])
      .catch(() => {
        this.loadFailed = true;
        this.toastr.danger('', 'רשימת המשתמשים לא נטענה');
      })
      .finally(() => this.loading = false);
  }

  /** Rows matching the search box: email, name, or a channel they hold a role on. */
  get filtered(): SuperAdminUser[] {
    const q = this.query.trim().toLowerCase();
    if (!q) return this.users;
    return this.users.filter(u =>
      (u.email || '').toLowerCase().includes(q)
      || (u.publicName || '').toLowerCase().includes(q)
      || (u.username || '').toLowerCase().includes(q)
      || Object.keys(u.channelRoles || {}).some(slug => slug.toLowerCase().includes(q)));
  }

  get operatorCount(): number {
    return this.users.filter(u => u.globalRole === 'super_admin').length;
  }

  channelRoles(user: SuperAdminUser): ChannelRoleChip[] {
    return Object.entries(user.channelRoles || {}).map(([slug, role]) => ({
      slug,
      label: (ROLE_LABELS as Record<string, string>)[role] || role,
    }));
  }

  getRoleLabel(role: string): string {
    return this.globalRoleLabels[role] || role;
  }

  /** First letter of the display name (or the address), for the avatar. */
  initial(user: SuperAdminUser): string {
    const s = (user.publicName || user.email || '').trim();
    return s ? s[0].toUpperCase() : '?';
  }
}
