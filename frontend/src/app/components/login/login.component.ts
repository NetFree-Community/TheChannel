import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../services/auth.service';


@Component({
  selector: 'app-login',
  imports: [FormsModule],
  templateUrl: './login.component.html',
  styleUrl: './login.component.scss'
})
export class LoginComponent implements OnInit {
  code: string = '';
  checkUserInfo: boolean = false;
  status!: 'failed';

  constructor(
    private _authService: AuthService,
    private _route: ActivatedRoute,
    private router: Router
  ) { }

  async ngOnInit() {
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

    this._route.queryParams.subscribe(params => {
      if (params['code'] && params['state'] === localStorage.getItem('google_oauth_state')) {
        this.code = params['code'];
        this.checkUserInfo = true;
        this._authService.login(this.code).then(async () => {
          await this._authService.loadUserInfo();
          this.redirectAfterLogin();
        }).catch(() => {
          this.code = '';
          this.status = 'failed';
          this.checkUserInfo = false;
          alert('התחברות נכשלה, נסה שוב');
        });
      }
    });
  }

  private redirectAfterLogin() {
    if (this._authService.userInfo?.globalRole === 'super_admin') {
      this.router.navigate(['/super-admin']);
      return;
    }

    const returnUrl = localStorage.getItem('returnUrl');
    localStorage.removeItem('returnUrl');
    if (returnUrl && !returnUrl.startsWith('/login')) {
      this.router.navigateByUrl(returnUrl);
      return;
    }

    this.router.navigate(['/channel']);
  }

  login() {
    this._authService.loginWithGoogle();
  }
}
