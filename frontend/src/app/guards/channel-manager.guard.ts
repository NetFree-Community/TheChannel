import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

/**
 * The manage page is moderator level and above (every tab of it is backed by
 * an endpoint gated at least that high). Anyone else who lands on the URL is
 * sent to the channel itself rather than to a page of 403 errors. The real
 * enforcement is the backend's; this only keeps the UI honest.
 */
export const ChannelManagerGuard: CanActivateFn = async (route) => {
  const router = inject(Router);
  const authService = inject(AuthService);
  const slug = route.paramMap.get('slug') ?? '';

  try {
    const user = await authService.loadUserInfo();
    const role = user?.channelRoles?.[slug];
    if (user?.globalRole === 'super_admin' || role === 'owner' || role === 'moderator') {
      return true;
    }
  } catch {
    // Not signed in or the request failed — fall through to the channel page,
    // whose own guard handles the login redirect.
  }
  return router.createUrlTree(['/channel', slug]);
};
