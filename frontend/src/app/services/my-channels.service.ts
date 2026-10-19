import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { MyChannel } from '../models/my-channel.model';
import { AuthService } from './auth.service';
import { User } from '../models/user.model';

/**
 * The channels of the signed-in user (every role, not only owner), with names
 * and logos — what the "my channels" page and the channel switcher in the
 * header show. Cached per user object: AuthService hands out the same User
 * instance until login/logout/reload replaces it, so a role change that goes
 * through reloadUserInfo() refreshes this list on the next call without any
 * explicit wiring.
 */
@Injectable({ providedIn: 'root' })
export class MyChannelsService {
  private cache?: Promise<MyChannel[]>;
  private cachedFor?: User;

  constructor(
    private http: HttpClient,
    private authService: AuthService,
  ) {}

  list(force = false): Promise<MyChannel[]> {
    const user = this.authService.userInfo;
    if (force || !this.cache || this.cachedFor !== user) {
      this.cachedFor = user;
      this.cache = firstValueFrom(this.http.get<MyChannel[]>('/api/my-channels'))
        .then(rows => rows ?? [])
        .catch(err => {
          // Never memoise a failure, or one blip hides the list until reload.
          this.cache = undefined;
          throw err;
        });
    }
    return this.cache;
  }

  /** Drop the cached list; the next list() fetches again. */
  invalidate(): void {
    this.cache = undefined;
    this.cachedFor = undefined;
  }
}
