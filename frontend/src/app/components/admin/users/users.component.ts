import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { 
  NbCardModule, 
  NbButtonModule, 
  NbToastrService,
  NbIconModule,
  NbInputModule,
  NbBadgeModule,
  NbListModule,
  NbUserModule,
  NbSpinnerModule,
  NbAlertModule
} from '@nebular/theme';
import { AdminService, Users } from '../../../services/admin.service';

@Component({
  selector: 'app-users',
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbInputModule,
    NbBadgeModule,
    NbListModule,
    NbUserModule,
    NbSpinnerModule,
    NbAlertModule
  ],
  templateUrl: './users.component.html',
  styleUrl: './users.component.scss'
})
export class UsersComponent implements OnInit {
  users: Users[] = [];
  filteredUsers: Users[] = [];
  loading = false;
  searchTerm = '';
  showOnlyBlocked = false;
  actionInProgress = new Set<string>();
  usersAmount: number = 0;

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService
  ) { }

  ngOnInit(): void {
    this.loadUsersAmount();
    this.loadUsers();
  }

  get totalUsers(): number {
    return this.users.length;
  }

  get blockedCount(): number {
    return this.users.filter(user => user.blocked).length;
  }

  async loadUsersAmount(): Promise<void> {
    try {
      this.usersAmount = await this.adminService.getUsersAmount();
    } catch (error) {
      console.error('Error loading users amount:', error);
    }
  }

  async loadUsers(): Promise<void> {
    this.loading = true;
    try {
      this.users = await this.adminService.getAllUsers();
      this.filterUsers();
      this.toastrService.success('', 'רשימת המשתמשים נטענה בהצלחה');
    } catch (error) {
      this.toastrService.danger('', 'שגיאה בטעינת רשימת המשתמשים');
      console.error('Error loading users:', error);
    } finally {
      this.loading = false;
    }
  }

  filterUsers(): void {
    let filtered = [...this.users];

    if (this.searchTerm.trim()) {
      const search = this.searchTerm.toLowerCase().trim();
      filtered = filtered.filter(user => 
        user.username?.toLowerCase().includes(search) ||
        user.email.toLowerCase().includes(search) ||
        user.publicName?.toLowerCase().includes(search)
      );
    }

    if (this.showOnlyBlocked) {
      filtered = filtered.filter(user => user.blocked);
    }

    this.filteredUsers = filtered;
  }

  toggleBlockedFilter(): void {
    this.showOnlyBlocked = !this.showOnlyBlocked;
    this.filterUsers();
  }

  async blockUser(user: Users): Promise<void> {
    if (user.isAdmin) {
      this.toastrService.warning('', 'לא ניתן לחסום משתמש מנהל');
      return;
    }

    const confirmed = confirm(`האם אתה בטוח שברצונך לחסום את ${user.username || user.email}?`);
    if (!confirmed) return;

    this.actionInProgress.add(user.email);
    try {
      await this.adminService.blockUser(user.email);
      user.blocked = true;
      this.filterUsers();
      this.toastrService.success('', `${user.username || user.email} נחסם בהצלחה`);
    } catch (error) {
      this.toastrService.danger('', 'שגיאה בחסימת המשתמש');
      console.error('Error blocking user:', error);
    } finally {
      this.actionInProgress.delete(user.email);
    }
  }

  async unblockUser(user: Users): Promise<void> {
    const confirmed = confirm(`האם אתה בטוח שברצונך לשחרר את ${user.username || user.email} מחסימה?`);
    if (!confirmed) return;

    this.actionInProgress.add(user.email);
    try {
      await this.adminService.unblockUser(user.email);
      user.blocked = false;
      this.filterUsers();
      this.toastrService.success('', `החסימה של ${user.username || user.email} הוסרה בהצלחה`);
    } catch (error) {
      this.toastrService.danger('', 'שגיאה בהסרת החסימה');
      console.error('Error unblocking user:', error);
    } finally {
      this.actionInProgress.delete(user.email);
    }
  }

  trackByEmail(index: number, user: Users): string {
    return user.email;
  }
}