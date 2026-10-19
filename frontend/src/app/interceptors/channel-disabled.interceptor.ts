import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { catchError, throwError } from 'rxjs';
import { ChannelStatusService } from '../services/channel-status.service';

const CHANNEL_API = /^\/api\/channel\/([^/?#]+)/;
// Only the channel's own info endpoint decides whether the channel exists:
// a 404 from any other route (a removed file, an unknown message id) says
// nothing about the channel itself.
const CHANNEL_INFO_API = /^\/api\/channel\/([^/?#]+)\/info$/;

/**
 * A disabled channel answers every /api/channel/{slug}/* request with
 * 403 {"error":"channel_disabled"}. Detecting it once here means every caller
 * benefits: the flag is raised centrally and ChannelComponent renders the
 * "הערוץ מושבת" page, instead of each service surfacing its own broken state
 * or a generic error toast.
 *
 * The error is still rethrown so existing per-caller handling (spinners,
 * catch blocks) keeps working unchanged.
 */
export const channelDisabledInterceptor: HttpInterceptorFn = (req, next) => {
  const channelStatus = inject(ChannelStatusService);

  return next(req).pipe(
    catchError((err: unknown) => {
      if (err instanceof HttpErrorResponse && err.status === 403) {
        const match = CHANNEL_API.exec(new URL(req.url, window.location.origin).pathname);
        if (match && isChannelDisabledBody(err.error)) {
          channelStatus.markDisabled(decodeURIComponent(match[1]));
        }
      }
      if (err instanceof HttpErrorResponse && err.status === 404) {
        const match = CHANNEL_INFO_API.exec(new URL(req.url, window.location.origin).pathname);
        if (match) {
          channelStatus.markNotFound(decodeURIComponent(match[1]));
        }
      }
      return throwError(() => err);
    })
  );
};

/**
 * The body arrives parsed when the response carried a JSON content type, but a
 * plain-text answer (or a blob request) leaves it as a string — accept both.
 */
function isChannelDisabledBody(body: unknown): boolean {
  if (typeof body === 'string') {
    return body.includes('channel_disabled');
  }
  return !!body && typeof body === 'object'
    && (body as { error?: unknown }).error === 'channel_disabled';
}
