import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { NbToastrService } from '@nebular/theme';
import { catchError, throwError } from 'rxjs';
import { AuthService } from '../services/auth.service';

// Routes only a signed-in operator ever calls: the super-admin panel and a
// channel's own management API. A 401 there is a session that has ended,
// never an anonymous reader (readers have no way to reach these calls).
const OPERATOR_API = /^\/api\/(super-admin\/|channel\/[^/?#]+\/admin\/)/;

// Several requests tend to fail together (a section loads two or three lists);
// one toast and one redirect are enough.
let lastRedirect = 0;

/**
 * A session that expires while the operator is mid-edit used to surface as
 * the generic "השמירה לא הצליחה, נסו שוב" of whichever screen made the call,
 * with no hint that signing in again is what is needed; the operator found
 * out on the next full page load. Detecting it once here covers every
 * super-admin and manage-page writer: say what happened, remember where they
 * were, and send them to sign in — the login page brings them back.
 *
 * The error is still rethrown so the caller's own handling (spinners,
 * dirty state) keeps working unchanged.
 */
export const sessionExpiredInterceptor: HttpInterceptorFn = (req, next) => {
  const router = inject(Router);
  const toastr = inject(NbToastrService);
  const authService = inject(AuthService);

  return next(req).pipe(
    catchError((err: unknown) => {
      if (err instanceof HttpErrorResponse && err.status === 401
        && OPERATOR_API.test(new URL(req.url, window.location.origin).pathname)
        && !router.url.startsWith('/login')
        && Date.now() - lastRedirect > 2000) {
        lastRedirect = Date.now();
        // Before the redirect: the login page re-checks the session, and with
        // the stale user still cached it would have sent the operator right
        // back to the screen whose request just failed.
        authService.sessionEnded();
        try {
          localStorage.setItem('returnUrl', router.url);
        } catch {
          // Storage unavailable — the login page falls back to /channel.
        }
        // The toast comes after the navigation: Nebular mounts its overlays
        // inside the page's <nb-layout>, and the panel's layout — with a toast
        // raised now — is destroyed by the route change a moment later.
        router.navigate(['/login']).then(() => setTimeout(() => toastr.warning('', 'ההתחברות פגה — יש להתחבר מחדש')));
      }
      return throwError(() => err);
    })
  );
};
