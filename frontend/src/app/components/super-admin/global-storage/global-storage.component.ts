import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbIconModule, NbToastrService
} from '@nebular/theme';
import { SuperAdminService } from '../../../services/super-admin.service';

@Component({
  selector: 'app-global-storage',
  standalone: true,
  imports: [
    CommonModule, FormsModule,
    NbCardModule, NbButtonModule, NbInputModule,
    NbFormFieldModule, NbIconModule
  ],
  template: `
    <nb-card>
      <nb-card-header>הגדרות אחסון גלובליות</nb-card-header>
      <nb-card-body>
        <p class="text-muted mb-3">
          ברירת המחדל הגלובלית לנפח אחסון לכל ערוץ. ניתן לשנות לכל ערוץ בנפרד מרשימת הערוצים.
        </p>
        <div class="d-flex align-items-center gap-3">
          <nb-form-field>
            <nb-icon nbPrefix icon="hard-drive-outline"></nb-icon>
            <input nbInput type="number" min="1" step="1"
                  [(ngModel)]="defaultQuotaGb"
                  placeholder="נפח ברירת מחדל (GB)">
          </nb-form-field>
          <span class="text-muted">GB לכל ערוץ</span>
          <button nbButton status="primary" (click)="save()" [disabled]="saving">
            {{ saving ? 'שומר...' : 'שמור' }}
          </button>
        </div>
      </nb-card-body>
    </nb-card>
  `
})
export class GlobalStorageComponent implements OnInit {
  defaultQuotaGb = 5;
  saving = false;

  constructor(
    private superAdminService: SuperAdminService,
    private toastr: NbToastrService
  ) {}

  async ngOnInit() {
    try {
      const cfg = await this.superAdminService.getGlobalStorageConfig();
      this.defaultQuotaGb = cfg.defaultQuotaGb;
    } catch {
      this.toastr.danger('שגיאה בטעינת הגדרות אחסון', 'שגיאה');
    }
  }

  async save() {
    this.saving = true;
    try {
      await this.superAdminService.setGlobalStorageConfig(this.defaultQuotaGb);
      this.toastr.success('הגדרות אחסון נשמרו', 'אחסון');
    } catch {
      this.toastr.danger('שגיאה בשמירה', 'שגיאה');
    } finally {
      this.saving = false;
    }
  }
}
