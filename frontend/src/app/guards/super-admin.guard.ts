import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

export const SuperAdminGuard: CanActivateFn = async (route, state) => {
  const router = inject(Router);
  const authService = inject(AuthService);

  try {
    const userInfo = await authService.loadUserInfo();
    if (userInfo?.globalRole === 'super_admin') {
      return true;
    }
    router.navigate(['/']);
    return false;
  } catch {
    router.navigate(['/']);
    return false;
  }
};
