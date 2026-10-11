import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from './auth.service';
import { ChatService } from './chat.service';
import { SlugService } from './slug.service';
import { User } from '../models/user.model';

/**
 * The backend never exposes a channel's `require_auth` flag as data: when the
 * feature is on, the `channelIfRequireAuth` middleware wraps every
 * /api/channel/{slug}/* route — including /info — with a login check, so an
 * anonymous visitor gets 401 there. A channel whose /info answers anonymously
 * is therefore public and has to stay reachable without logging in.
 */
const channelRequiresAuth = async (chatService: ChatService): Promise<boolean> => {
  try {
    await chatService.updateChannelInfo();
    return false;
  } catch (err: any) {
    return err?.status === 401;
  }
};

export const AuthGuard: CanActivateFn = async (route, state) => {
  const router = inject(Router);
  const authService = inject(AuthService);
  const chatService = inject(ChatService);
  const slugService = inject(SlugService);

  // The channel's /info is needed either way — by this guard for an anonymous
  // visitor, by every component right after — so it starts now, beside the
  // session check, instead of after it. The answer lands in ChatService's
  // cache, which initChannel keeps for this slug.
  const slug = route.paramMap.get('slug');
  let infoProbe: Promise<boolean> | undefined;
  if (slug) {
    slugService.slug = slug;
    chatService.clearCache();
    infoProbe = channelRequiresAuth(chatService);
  }

  let userInfo: User | null | undefined;
  try {
    userInfo = await authService.loadUserInfo();
  } catch (err: any) {
    // Only "not authenticated" is an answer about the session. Anything else
    // (server error, network blip, a proxy answering with HTML) says nothing
    // about who the visitor is, so let the route render and surface its own
    // error instead of bouncing the visitor through a login they don't need.
    if (err?.status !== 401) return true;
    userInfo = undefined;
  }

  if (userInfo) {
    // Settled before the route activates, so a missing or disabled channel is
    // already flagged when ChannelComponent initialises and it renders the
    // card at once. Without this the signed-in path (user-info is memoised,
    // so it resolved first) mounted the chat shell for a few frames, fired
    // eight requests for a channel that does not exist and logged them all.
    if (infoProbe) await infoProbe;
    return true;
  }

  // No session — either /api/user-info answered 401, or it resolved with an
  // empty body (204, a body-stripping proxy, an offline interstitial), which
  // leaves loadUserInfo() returning null. Both mean "anonymous", and both are
  // handled the same way below.
  //
  // A public channel stays open to anonymous visitors.
  if (infoProbe && !(await infoProbe)) return true;

  // Redirect via a UrlTree rather than `router.navigate(...) + false`.
  // Returning false cancels the navigation outright: the router then has no
  // active route to render and the visitor is left staring at a blank white
  // page with nothing in the console — which is exactly the bug this replaces.
  try {
    localStorage.setItem('returnUrl', state.url);
  } catch {
    // Storage can be unavailable (privacy mode, blocked cookies). Losing the
    // return URL is survivable; failing the guard is not.
  }
  return router.createUrlTree(['/login']);
};
