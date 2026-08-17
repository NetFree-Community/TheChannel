import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbBadgeModule, NbIconModule, NbToastrService, NbAlertModule, NbFormFieldModule
} from '@nebular/theme';
import { SuperAdminService, ChannelRequest } from '../../../services/super-admin.service';

@Component({
  selector: 'app-channel-requests',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbBadgeModule,
    NbIconModule,
    NbAlertModule,
    NbFormFieldModule,
  ],
  template: `
    <nb-card>
      <nb-card-header>
        <h5 class="mb-0">בקשות לפתיחת ערוץ</h5>
      </nb-card-header>
      <nb-card-body>
        @if (loading) {
          <p class="text-center">טוען...</p>
        } @else if (requests.length === 0) {
          <p class="text-center text-muted">אין בקשות</p>
        } @else {
          <div class="table-responsive" dir="rtl">
            <table class="table table-bordered align-middle">
              <thead class="table-light">
                <tr>
                  <th>שם</th>
                  <th>אימייל</th>
                  <th>Slug מבוקש</th>
                  <th>תיאור</th>
                  <th>תאריך</th>
                  <th>סטטוס</th>
                  <th>פעולות</th>
                </tr>
              </thead>
              <tbody>
                @for (req of requests; track req.id) {
                  <tr>
                    <td>{{ req.name }}</td>
                    <td>{{ req.email }}</td>
                    <td><code>{{ req.desiredSlug }}</code></td>
                    <td>
                      <span class="text-truncate d-inline-block" style="max-width:200px" [title]="req.description">
                        {{ req.description }}
                      </span>
                    </td>
                    <td>{{ req.createdAt | date:'dd/MM/yy HH:mm' }}</td>
                    <td>
                      @if (req.status === 'pending') {
                        <span class="badge bg-warning text-dark">ממתין</span>
                      } @else if (req.status === 'approved') {
                        <span class="badge bg-success">מאושר</span>
                        @if (req.approvedSlug) {
                          <br><small class="text-muted">{{ req.approvedSlug }}</small>
                        }
                      } @else if (req.status === 'rejected') {
                        <span class="badge bg-danger">נדחה</span>
                      }
                    </td>
                    <td>
                      @if (req.status === 'pending') {
                        <div class="d-flex gap-2">
                          <button nbButton status="success" size="small"
                            (click)="startApprove(req)"
                            [disabled]="actionId === req.id">
                            אשר
                          </button>
                          <button nbButton status="danger" size="small"
                            (click)="startReject(req)"
                            [disabled]="actionId === req.id">
                            דחה
                          </button>
                        </div>
                      } @else {
                        <span class="text-muted">—</span>
                      }
                    </td>
                  </tr>

                  <!-- Approve inline form -->
                  @if (actionId === req.id && approveMode) {
                    <tr class="table-success">
                      <td colspan="7">
                        <div class="p-3" dir="rtl">
                          <h6 class="mb-3">אישור בקשה — {{ req.name }}</h6>

                          @if (lastApproveResult && lastApproveResult.reqId === req.id) {
                            <nb-alert status="success" [closable]="false">
                              <p class="mb-1">✓ הערוץ נוצר בהצלחה!</p>
                              <p class="mb-1">Slug: <strong>{{ lastApproveResult.channelSlug }}</strong></p>
                              <p class="mb-1">אימייל הבעלים: <strong>{{ lastApproveResult.ownerEmail }}</strong></p>
                              <p class="mb-0">שלח לו את הקישור: <code>/channel/{{ lastApproveResult.channelSlug }}</code></p>
                            </nb-alert>
                          } @else {
                            <div class="row g-3">
                              <div class="col-md-4">
                                <label class="form-label">Slug סופי</label>
                                <input nbInput fullWidth
                                  type="text"
                                  [(ngModel)]="approveSlug"
                                  name="approveSlug"
                                  placeholder="my-channel"
                                  (input)="approveSlug = approveSlug.toLowerCase()">
                              </div>
                              <div class="col-md-8">
                                <label class="form-label">הערות (אופציונלי)</label>
                                <textarea nbInput fullWidth
                                  rows="2"
                                  [(ngModel)]="approveNotes"
                                  name="approveNotes"
                                  placeholder="הערות לבקשה..."></textarea>
                              </div>
                            </div>
                            <div class="d-flex gap-2 mt-3">
                              <button nbButton status="success"
                                (click)="confirmApprove()"
                                [disabled]="!approveSlug">
                                אשר ויצור ערוץ
                              </button>
                              <button nbButton status="basic" (click)="cancel()">ביטול</button>
                            </div>
                          }
                        </div>
                      </td>
                    </tr>
                  }

                  <!-- Reject inline form -->
                  @if (actionId === req.id && rejectMode) {
                    <tr class="table-danger">
                      <td colspan="7">
                        <div class="p-3" dir="rtl">
                          <h6 class="mb-3">דחיית בקשה — {{ req.name }}</h6>
                          <div class="mb-3">
                            <label class="form-label">הערות דחייה (אופציונלי)</label>
                            <textarea nbInput fullWidth
                              rows="2"
                              [(ngModel)]="rejectNotes"
                              name="rejectNotes"
                              placeholder="סיבת הדחייה..."></textarea>
                          </div>
                          <div class="d-flex gap-2">
                            <button nbButton status="danger" (click)="confirmReject()">דחה בקשה</button>
                            <button nbButton status="basic" (click)="cancel()">ביטול</button>
                          </div>
                        </div>
                      </td>
                    </tr>
                  }
                }
              </tbody>
            </table>
          </div>
        }
      </nb-card-body>
    </nb-card>
  `,
})
export class ChannelRequestsComponent implements OnInit {
  requests: ChannelRequest[] = [];
  loading = false;
  actionId = '';
  approveSlug = '';
  approveNotes = '';
  rejectNotes = '';
  approveMode = false;
  rejectMode = false;
  lastApproveResult: { reqId: string; channelSlug: string; ownerEmail: string } | null = null;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading = true;
    this.superAdminService.getChannelRequests()
      .then(reqs => { this.requests = reqs; })
      .catch(() => this.toastr.danger('', 'שגיאה בטעינת הבקשות'))
      .finally(() => { this.loading = false; });
  }

  startApprove(req: ChannelRequest): void {
    this.lastApproveResult = null;
    this.approveMode = true;
    this.rejectMode = false;
    this.actionId = req.id;
    this.approveSlug = req.desiredSlug.toLowerCase().replace(/[^a-z0-9-]/g, '-');
    this.approveNotes = '';
  }

  startReject(req: ChannelRequest): void {
    this.lastApproveResult = null;
    this.rejectMode = true;
    this.approveMode = false;
    this.actionId = req.id;
    this.rejectNotes = '';
  }

  confirmApprove(): void {
    if (!this.approveSlug) return;
    const id = this.actionId;
    this.superAdminService.approveChannelRequest(id, this.approveSlug, this.approveNotes)
      .then(result => {
        this.lastApproveResult = { reqId: id, channelSlug: result.channelSlug, ownerEmail: result.ownerEmail };
        this.toastr.success('', 'הערוץ נוצר בהצלחה');
        this.load();
      })
      .catch((err) => this.toastr.danger(err?.error || '', 'שגיאה באישור הבקשה'));
  }

  confirmReject(): void {
    const id = this.actionId;
    this.superAdminService.rejectChannelRequest(id, this.rejectNotes)
      .then(() => {
        this.toastr.success('', 'הבקשה נדחתה');
        this.cancel();
        this.load();
      })
      .catch(() => this.toastr.danger('', 'שגיאה בדחיית הבקשה'));
  }

  cancel(): void {
    this.actionId = '';
    this.approveMode = false;
    this.rejectMode = false;
    this.approveSlug = '';
    this.approveNotes = '';
    this.rejectNotes = '';
  }
}
