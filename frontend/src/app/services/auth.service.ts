import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { ResponseResult } from '../models/response-result.model';
import { User } from '../models/user.model';
interface GoogleAuthValues {
  googleOauthUrl: string;
  googleOauthScope: string;
  googleClientId: string;
}

@Injectable({
  providedIn: 'root'
})
export class AuthService {
  public userInfo?: User;
  // One in-flight /api/user-info shared by every concurrent caller: each
  // rendered message asks on first paint, and only a success was memoised, so
  // an anonymous visitor issued one request per message on the page and one
  // more per message arriving over SSE.
  private userInfoRequest?: Promise<User | undefined>;
  // The 401 that answered "anonymous", replayed to later callers (they branch
  // on err.status) until login/logout/reload drops it.
  private anonymousError?: unknown;

  constructor(
    private _http: HttpClient,
  ) { }

  async loginWithGoogle() {
    try {
      const googleAuthValues: GoogleAuthValues = await firstValueFrom(this._http.get<GoogleAuthValues>('/auth/google'));
      const state = crypto.randomUUID()
      const params = new URLSearchParams({
        client_id: googleAuthValues.googleClientId,
        redirect_uri: window.location.origin + '/login',
        scope: googleAuthValues.googleOauthScope,
        state: state,
        response_type: 'code',
        access_type: 'offline',
      });

      localStorage.setItem('google_oauth_state', state);
      window.location.href = `${googleAuthValues.googleOauthUrl}?${params.toString()}`;
    } catch (err: any) {
      throw err;
    }
  }

  async login(code: string) {
    // The login page's own check has just memoised "anonymous"; once the code
    // is exchanged for a session that answer is stale, and the page re-reads
    // user-info right after this call.
    this.forgetUserInfo();
    try {
      let res = await firstValueFrom(this._http.post<ResponseResult>('/auth/login', { code }));
      return res.success;
    } catch (err: any) {
      this.userInfo = undefined;
      throw err;
    }
  }

  async logout() {
    let res = await firstValueFrom(this._http.post<ResponseResult>('/auth/logout', {}));
    if (res.success) {
      this.forgetUserInfo();
    }
    return res.success;
  }

  async loadUserInfo(): Promise<User | undefined> {
    if (this.userInfo) return this.userInfo;
    if (this.anonymousError !== undefined) throw this.anonymousError;
    if (!this.userInfoRequest) {
      const request: Promise<User | undefined> = firstValueFrom(this._http.get<User>('/api/user-info'))
        .then(user => {
          this.userInfo = user;
          return user;
        }, (err: any) => {
          this.userInfo = undefined;
          if (err?.status === 401) this.anonymousError = err;
          throw err;
        })
        .finally(() => {
          // Only release our own slot: reloadUserInfo may already have started
          // a newer request that has to stay the shared one.
          if (this.userInfoRequest === request) this.userInfoRequest = undefined;
        });
      this.userInfoRequest = request;
    }
    return this.userInfoRequest;
  }

  /**
   * The server answered 401 to a signed-in call: the session ended behind the
   * cached user. Dropping the cache here matters — the login page and the
   * guards trust loadUserInfo(), and with the stale user still cached the
   * login page sent the operator straight back to the panel, whose first
   * request got 401 again, and so on in a loop.
   */
  sessionEnded() {
    this.forgetUserInfo();
  }

  private forgetUserInfo() {
    this.userInfo = undefined;
    this.anonymousError = undefined;
    this.userInfoRequest = undefined;
  }

  /**
   * loadUserInfo() memoises `userInfo`, so after something changes the user's
   * roles server-side (opening a channel makes them its owner) a plain re-read
   * hands back the stale object. Drop the cache first and refetch.
   */
  async reloadUserInfo() {
    this.forgetUserInfo();
    return this.loadUserInfo();
  }

  // Fire-and-forget visit registration: the channel-scoped user-info handler is
  // what writes the viewer into channel:<slug>:registered_emails, which feeds
  // the participant counter. Errors (e.g. anonymous visitor) are irrelevant.
  registerChannelVisit(slug: string): void {
    firstValueFrom(this._http.get<User>(`/api/channel/${slug}/user-info`)).catch(() => { });
  }
}
