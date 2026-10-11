import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { AdminService, ChannelUser } from '../../../services/admin.service';
import {
  NbButtonModule, NbCardModule, NbInputModule, NbToastrService, NbIconModule, NbSelectModule,
  NbSpinnerModule, NbTooltipModule,
} from "@nebular/theme";
import { FormsModule } from '@angular/forms';
import { AuthService } from '../../../services/auth.service';
import { SlugService } from '../../../services/slug.service';
import { ConfirmService } from '../../../services/confirm.service';

// Mirrors normEmail on the server, so the duplicate check here sees what the
// server will actually merge on.
function normalizeEmail(email: string | undefined): string {
  return (email ?? '').trim().toLowerCase();
}

// Deliberately loose: one '@' with something on both sides and a dot after it.
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

@Component({
  selector: 'app-privileg-dashboard',
  imports: [
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    FormsModule,
    NbIconModule,
    NbSelectModule,
    NbSpinnerModule,
    NbTooltipModule,
  ],
  templateUrl: './privileg-dashboard.component.html',
  styleUrl: './privileg-dashboard.component.scss'
})
export class PrivilegDashboardComponent implements OnInit {
  /** The users endpoint answered 403: the role changed since sign-in. */
  @Output() accessDenied = new EventEmitter<void>();

  constructor(
    private adminService: AdminService,
    private toastService: NbToastrService,
    public authService: AuthService,
    private slugService: SlugService,
    private confirm: ConfirmService,
  ) { }

  usersList: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  /** Addresses added since the last save, so the list can say "טרם נשמר". */
  pending = new Set<string>();
  newUserEmail = '';
  newUserRole: 'moderator' | 'writer' = 'writer';
  /** Inline validation under the email field (same wording the toasts used). */
  addError = '';

  loading = true;
  loadFailed = false;
  saving = false;
  private snapshot = '';

  readonly roleOptions = [
    { value: 'moderator', label: 'מנהל', hint: 'עריכה ומחיקה של כל ההודעות, פרטי הערוץ, דיווחים' },
    { value: 'writer', label: 'כותב', hint: 'פרסום הודעות בלבד' },
  ];

  /** What each role can do — read off the backend's role gates. */
  readonly roleCards = [
    {
      role: 'owner', label: 'בעלים', icon: 'star-outline',
      text: 'הכול: הגדרות, צוות, אחסון ופרסומות — וגם כל מה שמנהל יכול. נקבע בפתיחת הערוץ.',
    },
    {
      role: 'moderator', label: 'מנהל', icon: 'shield-outline',
      text: 'פרסום, עריכה ומחיקה של כל ההודעות בערוץ, פרטי הערוץ, אימוג\'ים, סטטיסטיקות ודיווחים.',
    },
    {
      role: 'writer', label: 'כותב', icon: 'edit-2-outline',
      text: 'פרסום הודעות, ועריכה או מחיקה של ההודעות שכתב בעצמו. לא רואה את מסך הניהול.',
    },
  ];

  get isOwner(): boolean {
    const roles = this.authService.userInfo?.channelRoles;
    if (this.authService.userInfo?.globalRole === 'super_admin') return true;
    return roles?.[this.slugService.slug] === 'owner';
  }

  get myEmail(): string {
    return normalizeEmail(this.authService.userInfo?.email);
  }

  get dirty(): boolean {
    return this.removedUsers.length > 0 || JSON.stringify(this.usersList) !== this.snapshot;
  }

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.loadFailed = false;
    this.adminService.getChannelUsers()
      .then(list => {
        this.usersList = list;
        this.removedUsers = [];
        this.pending.clear();
        this.snapshot = JSON.stringify(list);
      })
      .catch((err) => {
        this.loadFailed = true;
        if (err?.status === 403 || err?.status === 401) {
          this.accessDenied.emit();
          return;
        }
        this.toastService.danger('', 'לא הצלחנו לטעון את רשימת הצוות — נסו שוב');
      })
      .finally(() => this.loading = false);
  }

  isMe(email: string): boolean {
    return !!this.myEmail && normalizeEmail(email) === this.myEmail;
  }

  isPending(email: string): boolean {
    return this.pending.has(normalizeEmail(email));
  }

  initial(email: string): string {
    return (email || '?').trim().charAt(0).toUpperCase();
  }

  saveChanges() {
    if (this.saving) return;
    // The server applies the list in order, last write wins — so a removal that
    // trailed a re-grant of the same address ("delete alice, add alice back as
    // writer") revoked her. Removals go first, and one whose address is being
    // granted again is dropped altogether.
    const granted = new Set(this.usersList.map(u => normalizeEmail(u.email)));
    const removals = this.removedUsers.filter(u => !granted.has(normalizeEmail(u.email)));
    this.saving = true;
    this.adminService.setChannelUsers([...removals, ...this.usersList])
      .then(() => {
        this.removedUsers = [];
        this.pending.clear();
        this.snapshot = JSON.stringify(this.usersList);
        this.toastService.success('', 'הצוות עודכן');
      })
      .catch((err) => {
        const text = typeof err?.error === 'string' ? err.error : '';
        if (err?.status === 400 && text.includes('invalid email')) {
          this.toastService.danger('', 'אחת הכתובות ברשימה אינה כתובת מייל תקינה');
        } else if (err?.status === 403 || err?.status === 401) {
          this.toastService.danger('', 'רק בעלי הערוץ יכולים לשנות את הצוות');
        } else {
          this.toastService.danger('', 'שמירת הצוות נכשלה — נסו שוב');
        }
      })
      .finally(() => this.saving = false);
  }

  async deleteUser(index: number) {
    const user = this.usersList[index];
    if (!user) return;
    const ok = await this.confirm.ask({
      title: `להסיר את ${user.email}?`,
      message: 'ההרשאה תוסר בלחיצה על "שמירה". ההודעות שפורסמו נשארות בערוץ.',
      confirmLabel: 'הסרה',
      status: 'danger',
      icon: 'person-remove-outline',
    });
    if (!ok) return;
    const [removed] = this.usersList.splice(index, 1);
    if (removed?.email) {
      this.removedUsers.push({ email: removed.email, role: '' });
      this.pending.delete(normalizeEmail(removed.email));
    }
  }

  saveNewUser() {
    // The server normalises and silently skips a blank address, and stores an
    // invalid one for good — where it can never match a Google login. Catch the
    // typo here, while the owner is still looking at it.
    this.addError = '';
    const email = normalizeEmail(this.newUserEmail);
    if (!email) {
      this.addError = 'יש להזין כתובת מייל';
      return;
    }
    if (!EMAIL_SHAPE.test(email)) {
      this.addError = 'כתובת המייל אינה תקינה';
      return;
    }
    if (this.usersList.some(u => normalizeEmail(u.email) === email)) {
      this.addError = 'כתובת המייל כבר קיימת ברשימה';
      return;
    }
    this.usersList.push({ email, role: this.newUserRole });
    this.pending.add(email);
    this.newUserEmail = '';
    this.newUserRole = 'writer';
  }

  resetNewUser() {
    this.newUserEmail = '';
    this.newUserRole = 'writer';
    this.addError = '';
  }

  getRoleLabel(role: string): string {
    if (role === 'owner') return 'בעלים';
    return this.roleOptions.find(o => o.value === role)?.label ?? role;
  }
}
