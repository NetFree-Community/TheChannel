import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

// Returns UrlTrees instead of navigating itself: a visitor whose session ended
// mid-edit used to be bounced to the marketing page with no word about it.
// AuthGuard runs first on this route and stores the returnUrl, so /login
// brings the operator back here after signing in.
export const SuperAdminGuard: CanActivateFn = async () => {
  const router = inject(Router);
  const authService = inject(AuthService);

  try {
    const userInfo = await authService.loadUserInfo();
    if (userInfo?.globalRole === 'super_admin') {
      return true;
    }
    // Signed in, but not an operator.
    return router.createUrlTree(['/']);
  } catch {
    return router.createUrlTree(['/login']);
  }
};
