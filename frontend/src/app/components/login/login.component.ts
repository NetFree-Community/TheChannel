import { Component, HostListener, OnDestroy, OnInit } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import {
  NbAlertModule, NbButtonModule, NbCardModule, NbIconModule, NbLayoutModule, NbSpinnerModule,
} from '@nebular/theme';
import { Subscription } from 'rxjs';
import { AuthService } from '../../services/auth.service';

/**
 * The sign-in page. There is no password: the only way in is a Google
 * account, which Google sends back here with a one-time code that the server
 * swaps for a session. The page therefore has three states — the button, the
 * "connecting" spinner while that swap runs, and a failure with a retry —
 * and otherwise only decides where to send the user afterwards.
 */
@Component({
  selector: 'app-login',
  standalone: true,
  imports: [
    RouterLink,
    NbLayoutModule,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbAlertModule,
    NbSpinnerModule,
  ],
  templateUrl: './login.component.html',
  styleUrl: './login.component.scss'
})
export class LoginComponent implements OnInit, OnDestroy {
  code: string = '';
  checkUserInfo: boolean = false;
  status!: 'failed';
  /** What went wrong, in the user's words; shown with the retry button. */
  errorMessage = '';
  /** True while the user is being sent to Google (the button was pressed). */
  redirecting = false;

  private paramsSub?: Subscription;

  constructor(
    private _authService: AuthService,
    private _route: ActivatedRoute,
    private router: Router,
    private titleService: Title,
  ) { }

  /** The spinner's caption: the initial session check vs. the code exchange. */
  get busyText(): string {
    if (this.code) return 'מחברים אתכם…';
    if (this.redirecting) return 'עוברים לגוגל…';
    return 'רק רגע…';
  }

  get busy(): boolean {
    return !!this.code || this.checkUserInfo || this.redirecting;
  }

  async ngOnInit() {
    // index.html ships an empty <title>; without this the tab shows the URL.
    this.titleService.setTitle('התחברות · הערוץ');
    this.checkUserInfo = true;

    try {
      await this._authService.loadUserInfo();
      if (this._authService.userInfo) {
        this.checkUserInfo = false;
        this.redirectAfterLogin();
        return;
      }
    } catch {
      // Not logged in — check for OAuth callback params
    }

    this.checkUserInfo = false;

    this.paramsSub = this._route.queryParams.subscribe(params => {
      if (params['error']) {
        // Google sends the user back with error=access_denied when they cancel
        // the consent screen (and other error codes for a misconfigured app).
        // Only params['code'] used to be inspected, so this showed the login
        // button again as if nothing had happened.
        this.clearOauthState();
        this.fail(params['error'] === 'access_denied'
          ? 'ההתחברות בוטלה לפני שהסתיימה. אפשר לנסות שוב.'
          : 'גוגל לא השלימה את ההתחברות. נסו שוב — ואם זה חוזר, פנו אלינו מדף הבית.');
        return;
      }
      if (params['code'] && params['state'] !== localStorage.getItem('google_oauth_state')) {
        // Google sent us back but the anti-CSRF state does not match what this
        // browser stored (storage cleared, a second tab, a replayed link).
        // Previously this fell through silently and the page just showed the
        // login button again, as if nothing had happened.
        this.fail('ההתחברות לא הושלמה: הקישור פג תוקף או נפתח בדפדפן אחר. נסו להתחבר שוב מכאן.');
        return;
      }
      if (params['code'] && params['state'] === localStorage.getItem('google_oauth_state')) {
        this.code = params['code'];
        this.checkUserInfo = true;
        this.errorMessage = '';
        // The state is single-use: once the code is consumed the callback URL
        // must stop matching, otherwise the Back button (after a logout) lands
        // on this history entry and re-posts the already-spent code, which the
        // server rejects with 500 and the user sees a failure they did not cause.
        this.clearOauthState();
        this._authService.login(this.code).then(async () => {
          await this._authService.loadUserInfo();
          this.redirectAfterLogin();
        }).catch(() => {
          this.code = '';
          this.checkUserInfo = false;
          this.fail('ההתחברות נכשלה בדרך חזרה מגוגל. נסו שוב — ואם זה חוזר, פנו אלינו מדף הבית.');
        });
      }
    });
  }

  ngOnDestroy(): void {
    this.paramsSub?.unsubscribe();
  }

  private fail(message: string): void {
    this.status = 'failed';
    this.errorMessage = message;
  }

  private clearOauthState() {
    try {
      localStorage.removeItem('google_oauth_state');
    } catch {
      // Storage unavailable — nothing was stored to begin with.
    }
  }

  private redirectAfterLogin() {
    // Consumed for every role, before any early return: the super-admin branch
    // used to return first, leaving the stored URL behind for the next
    // (non-admin) login on the same browser, which was then dropped into a
    // channel it never asked for.
    const returnUrl = localStorage.getItem('returnUrl');
    localStorage.removeItem('returnUrl');
    const hasReturnUrl = !!returnUrl && !returnUrl.startsWith('/login');

    if (this._authService.userInfo?.globalRole === 'super_admin') {
      // A super admin who signed in from a channel page wants that page back,
      // not the panel; the panel is only the default.
      if (hasReturnUrl) {
        this.router.navigateByUrl(returnUrl!);
      } else {
        this.router.navigate(['/super-admin']);
      }
      return;
    }

    if (hasReturnUrl) {
      this.router.navigateByUrl(returnUrl!);
      return;
    }

    this.router.navigate(['/channel']);
  }

  // Back from Google's account picker restores this page from the
  // back-forward cache with its state intact — i.e. the spinner that replaced
  // the button. A restored page is idle again.
  @HostListener('window:pageshow', ['$event'])
  onPageShow(event: PageTransitionEvent) {
    if (event.persisted) this.redirecting = false;
  }

  async login() {
    this.errorMessage = '';
    this.redirecting = true;
    try {
      await this._authService.loginWithGoogle();
    } catch {
      // GET /auth/google failed; without this the click did nothing at all.
      this.redirecting = false;
      this.fail('ההתחברות אינה זמינה כרגע. בדקו את החיבור לאינטרנט ונסו שוב בעוד רגע.');
    }
  }
}
