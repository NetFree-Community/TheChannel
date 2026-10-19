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
  NbTooltipModule,
} from '@nebular/theme';
import { SuperAdminService, ChannelUser } from '../../../services/super-admin.service';

/** The backend normalises emails (trim + lowercase) before comparing. */
function sameEmail(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}

// Deliberately loose, and the same shape the owner-side privileges screen
// checks: one '@' with something on both sides and a dot after it.
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

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
    NbTooltipModule,
  ],
  templateUrl: './channel-users.component.html',
  styleUrl: './channel-users.component.scss',
})
export class ChannelUsersComponent implements OnInit {
  @Input() slug!: string;

  users: ChannelUser[] = [];
  // Removed users are sent with an empty role so the server actually revokes them —
  // an email that is simply missing from the payload is never touched.
  removedUsers: ChannelUser[] = [];
  /** The role each removed user had, so "undo" puts them back as they were. */
  private removedRoles = new Map<string, string>();
  loading = true;
  loadFailed = false;
  saving = false;
  addingUser = false;
  newEmail = '';
  newRole: 'owner' | 'moderator' | 'writer' | '' = 'moderator';
  /** JSON of the last server copy, to tell the operator about unsaved edits. */
  private snapshot = '';

  roleOptions: { value: string; label: string; hint: string }[] = [
    { value: 'owner', label: 'בעלים', hint: 'שליטה מלאה: הגדרות, משתמשים, כתיבה ומחיקה.' },
    { value: 'moderator', label: 'מנהל', hint: 'ניהול הערוץ וכתיבה, בלי למנות בעלים.' },
    { value: 'writer', label: 'כותב', hint: 'פרסום הודעות בלבד.' },
  ];

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.loadUsers();
  }

  loadUsers() {
    this.loading = true;
    this.loadFailed = false;
    this.superAdminService.getChannelUsers(this.slug)
      .then(users => {
        this.users = [...(users || [])];
        this.removedUsers = [];
        this.removedRoles.clear();
        this.snapshot = JSON.stringify(this.users);
      })
      .catch((err) => {
        this.loadFailed = true;
        this.toastr.danger('', err?.status === 404
          ? 'הערוץ לא נמצא — ייתכן שנמחק'
          : 'רשימת המשתמשים לא נטענה');
      })
      .finally(() => this.loading = false);
  }

  get dirty(): boolean {
    return this.removedUsers.length > 0 || JSON.stringify(this.users) !== this.snapshot;
  }

  roleHint(role: string): string {
    return this.roleOptions.find(o => o.value === role)?.hint || '';
  }

  /** First letter of the address, for the avatar. */
  initial(email: string): string {
    const e = (email || '').trim();
    return e ? e[0].toUpperCase() : '?';
  }

  addUser() {
    const email = this.newEmail.trim();
    if (!email) {
      this.toastr.warning('', 'יש להזין כתובת אימייל');
      return;
    }
    // A typo is caught while the operator is still looking at it; stored, it
    // would never match a Google login and the row would just sit there.
    if (!EMAIL_SHAPE.test(email)) {
      this.toastr.warning('', 'כתובת המייל אינה תקינה');
      return;
    }
    if (this.users.some(u => sameEmail(u.email, email))) {
      this.toastr.warning('', 'המשתמש כבר ברשימה — שנו את התפקיד שלו בשורה הקיימת');
      return;
    }
    // Remove-then-re-add before saving is how a role change is done here; the
    // queued revoke must not survive, or (applied after the grant) it would
    // silently strip the user while the screen still shows them with a role.
    this.removedUsers = this.removedUsers.filter(u => !sameEmail(u.email, email));
    this.users.push({ email, role: this.newRole as any });
    this.newEmail = '';
    this.newRole = 'moderator';
    this.addingUser = false;
  }

  cancelAdd() {
    this.addingUser = false;
    this.newEmail = '';
    this.newRole = 'moderator';
  }

  removeUser(index: number) {
    const [removed] = this.users.splice(index, 1);
    if (!removed?.email) return;
    this.removedRoles.set(removed.email.trim().toLowerCase(), removed.role);
    this.removedUsers.push({ email: removed.email, role: '' });
  }

  /** Puts a just-removed user back, with the role they had, before anything was saved. */
  undoRemove(index: number) {
    const [restored] = this.removedUsers.splice(index, 1);
    if (!restored?.email) return;
    const key = restored.email.trim().toLowerCase();
    const role = this.removedRoles.get(key) || 'writer';
    this.removedRoles.delete(key);
    if (!this.users.some(u => sameEmail(u.email, restored.email))) {
      this.users.push({ email: restored.email, role: role as any });
    }
  }

  save() {
    this.saving = true;
    // The server applies changes in order and the last one for an email wins,
    // so revokes go first and a revoke of an email that is also granted is
    // dropped — a grant must never be undone by an earlier removal.
    const removals = this.removedUsers.filter(
      r => !this.users.some(u => sameEmail(u.email, r.email)),
    );
    this.superAdminService.setChannelUsers(this.slug, [...removals, ...this.users])
      .then(() => {
        this.removedUsers = [];
        this.removedRoles.clear();
        this.snapshot = JSON.stringify(this.users);
        this.toastr.success('', 'משתמשי הערוץ נשמרו');
      })
      .catch((err) => {
        // The server refuses a malformed address with 400 'invalid email' —
        // name the field rather than a generic failure.
        const text = typeof err?.error === 'string' ? err.error.toLowerCase() : '';
        if (err?.status === 400 && text.includes('invalid email')) {
          this.toastr.danger('', 'אחת מכתובות המייל אינה תקינה');
        } else if (err?.status === 404) {
          this.toastr.danger('', 'הערוץ לא נמצא — ייתכן שנמחק');
        } else {
          this.toastr.danger('', 'השמירה לא הצליחה, נסו שוב');
        }
      })
      .finally(() => this.saving = false);
  }

  getRoleLabel(role: string): string {
    const opt = this.roleOptions.find(o => o.value === role);
    return opt ? opt.label : role;
  }
}
