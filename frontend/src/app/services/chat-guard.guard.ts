import { inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { CanActivateFn, Router } from '@angular/router';
import { firstValueFrom } from 'rxjs';
import { AuthService } from './auth.service';

/**
 * The backend never exposes a channel's `require_auth` flag as data: when the
 * feature is on, the `channelIfRequireAuth` middleware wraps every
 * /api/channel/{slug}/* route — including /info — with a login check, so an
 * anonymous visitor gets 401 there. A channel whose /info answers anonymously
 * is therefore public and has to stay reachable without logging in.
 */
const channelRequiresAuth = async (http: HttpClient, slug: string): Promise<boolean> => {
  try {
    await firstValueFrom(http.get(`/api/channel/${slug}/info`));
    return false;
  } catch (err: any) {
    return err?.status === 401;
  }
};

export const AuthGuard: CanActivateFn = async (route, state) => {
  const router = inject(Router);
  const authService = inject(AuthService);
  const http = inject(HttpClient);

  try {
    let userInfo = await authService.loadUserInfo();
    if (userInfo) return true;
  } catch (err: any) {
    if (err.status === 401) {
      // Anonymous visitor: only send them to the login page when the channel
      // actually requires auth. Public channels stay open.
      const slug = route.paramMap.get('slug');
      if (slug && !(await channelRequiresAuth(http, slug))) return true;

      localStorage.setItem('returnUrl', state.url);
      router.navigate(['/login']);
      return false;
    }
    return true;
  }

  return false;
};
