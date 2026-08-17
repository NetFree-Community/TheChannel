import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterLink, Router } from '@angular/router';
import {
  NbCardModule, NbButtonModule, NbInputModule,
  NbFormFieldModule, NbIconModule, NbAlertModule, NbSpinnerModule,
  NbLayoutModule
} from '@nebular/theme';
import { AuthService } from '../../services/auth.service';
import { ChannelRequestService } from '../../services/channel-request.service';

@Component({
  selector: 'app-landing-page',
  standalone: true,
  imports: [
    CommonModule,
    FormsModule,
    RouterLink,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbFormFieldModule,
    NbIconModule,
    NbAlertModule,
    NbSpinnerModule,
    NbLayoutModule,
  ],
  styleUrl: './landing-page.component.scss',
  template: `
    <!-- nb-layout must wrap the page (and sit outside the isChecking guard) —
         it is what applies the nb-theme-* class that every Nebular CSS variable
         hangs off. Without it the whole page renders unstyled. -->
    <nb-layout>
      <nb-layout-column>
    @if (!isChecking) {
    <!-- Navbar -->
    <nav class="d-flex justify-content-between align-items-center px-4 py-3 border-bottom bg-white">
      <a nbButton status="primary" [routerLink]="['/login']">התחבר</a>
      <span class="fw-bold fs-5">TheChannel</span>
    </nav>

    <!-- Hero Section -->
    <section class="hero">
      <div class="container">
        <h1>פתחו ערוץ חדשות משלכם</h1>
        <p>הגישו בקשה בחינם ונפתח לכם ערוץ פרטי תוך 24 שעות</p>
        <a nbButton status="control" size="large" href="#request-form" (click)="scrollToForm($event)">
          בקש ערוץ עכשיו
        </a>
      </div>
    </section>

    <!-- How it works -->
    <section class="section bg-light">
      <div class="container">
        <h2 class="section-title">איך זה עובד?</h2>
        <div class="row g-4 justify-content-center">
          <div class="col-md-4">
            <div class="card text-center h-100 border-0 shadow-sm p-4">
              <div class="step-number">1</div>
              <nb-icon icon="edit-2-outline" class="fs-1 text-primary mb-2" style="font-size:2rem"></nb-icon>
              <h5 class="mt-2">מלא טופס</h5>
            </div>
          </div>
          <div class="col-md-4">
            <div class="card text-center h-100 border-0 shadow-sm p-4">
              <div class="step-number">2</div>
              <nb-icon icon="checkmark-circle-outline" class="text-primary mb-2" style="font-size:2rem"></nb-icon>
              <h5 class="mt-2">קבל אישור</h5>
            </div>
          </div>
          <div class="col-md-4">
            <div class="card text-center h-100 border-0 shadow-sm p-4">
              <div class="step-number">3</div>
              <nb-icon icon="radio-outline" class="text-primary mb-2" style="font-size:2rem"></nb-icon>
              <h5 class="mt-2">התחל לשדר</h5>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- Features Section -->
    <section class="section">
      <div class="container">
        <h2 class="section-title">מה מקבלים?</h2>
        <div class="row g-4">
          <div class="col-md-6">
            <div class="card border-0 shadow-sm p-4 h-100 d-flex flex-row align-items-start gap-3">
              <nb-icon icon="brush-outline" style="font-size:1.8rem; color:#3366ff; flex-shrink:0"></nb-icon>
              <div>
                <h5 class="mb-1">עיצוב מותאם אישית</h5>
                <p class="text-muted mb-0">התאימו את הערוץ שלכם לסגנון שלכם</p>
              </div>
            </div>
          </div>
          <div class="col-md-6">
            <div class="card border-0 shadow-sm p-4 h-100 d-flex flex-row align-items-start gap-3">
              <nb-icon icon="settings-2-outline" style="font-size:1.8rem; color:#3366ff; flex-shrink:0"></nb-icon>
              <div>
                <h5 class="mb-1">ניהול קל ופשוט</h5>
                <p class="text-muted mb-0">פאנל ניהול נוח ופשוט לשימוש</p>
              </div>
            </div>
          </div>
          <div class="col-md-6">
            <div class="card border-0 shadow-sm p-4 h-100 d-flex flex-row align-items-start gap-3">
              <nb-icon icon="hard-drive-outline" style="font-size:1.8rem; color:#3366ff; flex-shrink:0"></nb-icon>
              <div>
                <h5 class="mb-1">אחסון מדיה</h5>
                <p class="text-muted mb-0">העלו תמונות וקבצים ישירות לערוץ</p>
              </div>
            </div>
          </div>
          <div class="col-md-6">
            <div class="card border-0 shadow-sm p-4 h-100 d-flex flex-row align-items-start gap-3">
              <nb-icon icon="globe-outline" style="font-size:1.8rem; color:#3366ff; flex-shrink:0"></nb-icon>
              <div>
                <h5 class="mb-1">ממשק בעברית</h5>
                <p class="text-muted mb-0">מבנה RTL מלא, תמיכה מושלמת בעברית</p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- Request Form -->
    <section class="section bg-light" id="request-form">
      <div class="container">
        <div class="row justify-content-center">
          <div class="col-md-7">
            <nb-card>
              <nb-card-header>
                <h4 class="mb-0">בקש ערוץ</h4>
              </nb-card-header>
              <nb-card-body>
                @if (submitted) {
                  <nb-alert status="success" [closable]="false">
                    הבקשה נשלחה! נחזור אליך בהקדם
                  </nb-alert>
                } @else {
                  @if (error) {
                    <nb-alert status="danger" [closable]="false">{{ error }}</nb-alert>
                  }
                  <form (ngSubmit)="submitRequest()" #requestForm="ngForm">
                    <div class="mb-3">
                      <label class="form-label">שם מלא</label>
                      <input nbInput fullWidth
                        type="text"
                        [(ngModel)]="reqName"
                        name="reqName"
                        required
                        placeholder="שם מלא">
                    </div>
                    <div class="mb-3">
                      <label class="form-label">כתובת אימייל</label>
                      <input nbInput fullWidth
                        type="email"
                        [(ngModel)]="reqEmail"
                        name="reqEmail"
                        required
                        placeholder="your@email.com">
                    </div>
                    <div class="mb-3">
                      <label class="form-label">Slug רצוי</label>
                      <input nbInput fullWidth
                        type="text"
                        [(ngModel)]="reqSlug"
                        name="reqSlug"
                        required
                        placeholder="my-channel — אותיות קטנות ומקפים בלבד">
                    </div>
                    <div class="mb-3">
                      <label class="form-label">תיאור מה תעשה עם הערוץ</label>
                      <textarea nbInput fullWidth
                        rows="4"
                        [(ngModel)]="reqDescription"
                        name="reqDescription"
                        required
                        placeholder="ספר לנו על הערוץ שלך..."></textarea>
                    </div>
                    <button nbButton status="primary" fullWidth type="submit"
                      [disabled]="submitting">
                      @if (submitting) {
                        <nb-spinner size="small" status="control"></nb-spinner>
                      }
                      שלח בקשה
                    </button>
                  </form>
                }
              </nb-card-body>
            </nb-card>
          </div>
        </div>
      </div>
    </section>

    <!-- Footer -->
    <footer class="footer">
      <span>TheChannel &copy; 2024</span>
    </footer>
    } <!-- end @if (!isChecking) -->
      </nb-layout-column>
    </nb-layout>
  `,
})
export class LandingPageComponent implements OnInit {
  isChecking = true;
  reqName = '';
  reqEmail = '';
  reqSlug = '';
  reqDescription = '';
  submitting = false;
  submitted = false;
  error = '';

  constructor(
    private authService: AuthService,
    private channelRequestService: ChannelRequestService,
    private router: Router,
  ) {}

  async ngOnInit() {
    try {
      const user = await this.authService.loadUserInfo();
      if (user) {
        if (user.globalRole === 'super_admin') {
          this.router.navigate(['/super-admin']);
        } else {
          this.router.navigate(['/channel']);
        }
        return;
      }
    } catch {
      // Not logged in — show the landing page
    }
    this.isChecking = false;
  }

  scrollToForm(event: Event) {
    event.preventDefault();
    const el = document.getElementById('request-form');
    if (el) {
      el.scrollIntoView({ behavior: 'smooth' });
    }
  }

  async submitRequest() {
    this.error = '';
    this.submitting = true;
    try {
      await this.channelRequestService.submitRequest(
        this.reqName,
        this.reqEmail,
        this.reqSlug,
        this.reqDescription,
      );
      this.submitted = true;
    } catch (err: any) {
      this.error = err?.error?.message || 'שגיאה בשליחת הבקשה. נסה שוב.';
    } finally {
      this.submitting = false;
    }
  }
}
