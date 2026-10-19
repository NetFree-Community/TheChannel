import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { NbToastrService } from '@nebular/theme';
import { firstValueFrom } from 'rxjs';
import type { FirebaseApp, FirebaseOptions } from 'firebase/app';
import { ResponseResult } from '../models/response-result.model';
import { SlugService } from './slug.service';


interface NotificationsConfig {
  enableNotifications: boolean,
  vapid?: string,
  firebaseConfig?: FirebaseOptions,
}

@Injectable({
  providedIn: 'root'
})
export class NotificationsService {
  public initialized = false;
  private app: FirebaseApp | null = null;
  private messaging: any;
  public config: NotificationsConfig | null = null;

  // Firebase is ~150 KB of JavaScript that only matters on a channel whose
  // operator switched push on and entered FCM keys. Imported on demand at that
  // point, so every other channel (and the landing, login and manage pages)
  // never downloads it. Both modules are fetched together, once.
  private firebase?: Promise<{
    app: typeof import('firebase/app');
    messaging: typeof import('firebase/messaging');
  }>;

  private loadFirebase() {
    if (!this.firebase) {
      this.firebase = Promise.all([import('firebase/app'), import('firebase/messaging')])
        .then(([app, messaging]) => ({ app, messaging }))
        .catch(err => {
          // A failed chunk download must not poison every later attempt.
          this.firebase = undefined;
          throw err;
        });
    }
    return this.firebase;
  }

  constructor(
    private http: HttpClient,
    private tostrService: NbToastrService,
    private slugService: SlugService,
  ) { }

  // Cleared on channel switch: the push toggle is per channel, so the state
  // fetched for the previous channel must not leak into the next one.
  reset() {
    this.initialized = false;
    this.config = null;
    this.app = null;
    this.messaging = undefined;
  }

  async init() {
    // Called at bootstrap too, before any channel slug is resolved.
    if (!this.slugService.slug) return;
    if (this.initialized) return;

    // Nobody awaits init() and reset() cannot cancel a request already in
    // flight: switching channels while the previous channel's config was
    // loading used to store that config (and initialized=true) for the new
    // channel, whose own init() then returned early — the bell of channel A
    // on channel B, subscribing the device to A. Same guard as chat.service.
    const requestedSlug = this.slugService.slug;
    const config = await firstValueFrom(this.http.get<NotificationsConfig>(`/api/channel/${requestedSlug}/notifications-config`));
    if (requestedSlug !== this.slugService.slug) return;
    this.config = config;

    if (!this.config) return;

    if (this.config.enableNotifications) {
      // The server always emits a firebaseConfig object, with empty strings
      // when the operator never entered the FCM keys, so testing for the
      // object itself never fired: initializeApp({apiKey: ''}) succeeded,
      // the bell appeared, and every tap failed in getToken with no VAPID.
      if (!this.config.firebaseConfig?.apiKey || !this.config.vapid) return;

      // Safari on iOS outside a home-screen app, and any browser without
      // service workers or the Push API, has no Messaging at all: getMessaging()
      // throws there, and since nobody awaits init() the throw surfaced as an
      // unhandled rejection on every channel load. Ask first, and keep the bell
      // hidden where it could never work.
      let fb: Awaited<ReturnType<NotificationsService['loadFirebase']>>;
      try {
        fb = await this.loadFirebase();
      } catch (err) {
        console.warn('Push notifications unavailable: firebase failed to load', err);
        return;
      }
      if (!(await fb.messaging.isSupported().catch(() => false))) return;
      // More awaits — the channel may have changed meanwhile.
      if (requestedSlug !== this.slugService.slug) return;

      try {
        // initializeApp() refuses a second default app with different options;
        // reuse the one from a previous channel visit instead of tripping it.
        this.app = fb.app.getApps().length ? fb.app.getApp() : fb.app.initializeApp(this.config.firebaseConfig);
        this.messaging = fb.messaging.getMessaging(this.app);
      } catch (err) {
        console.warn('Push notifications unavailable in this browser', err);
        return;
      }
      this.initialized = true;

      fb.messaging.onMessage(this.messaging, (payload) => {
        //this.tostrService.success("", 'התראה חדשה!');
      });
      return;
    }
    return;
  }


  async requestPermission() {
    if (typeof Notification === 'undefined' || !this.messaging) {
      this.tostrService.warning("", 'הדפדפן הזה אינו תומך בהתראות');
      return;
    }

    // Permission is per browser origin, and every channel on this platform
    // shares one origin. Being 'granted' therefore only says the browser may
    // show notifications — it says nothing about THIS channel, whose
    // subscription list is separate. Enabling push on one channel and then
    // tapping the bell on another used to answer "already subscribed" while
    // never registering the device with the second channel at all.
    if (Notification.permission === 'granted') {
      this.subscribeThisDevice('ההתראות לערוץ זה פעילות');
      return;
    }

    // Once denied, the browser answers 'denied' straight away without asking
    // again — so without this the bell simply did nothing when tapped.
    if (Notification.permission === 'denied') {
      this.tostrService.warning("", 'ההתראות חסומות בהגדרות הדפדפן לאתר זה');
      return;
    }

    Notification.requestPermission()
      .then((permission) => {
        if (permission === 'granted') {
          this.subscribeThisDevice('התראות הופעלו בהצלחה!');
        }
      });
  }

  /**
   * Registers this device's push token with the current channel. Safe to
   * repeat: the server keeps one entry per token.
   */
  private subscribeThisDevice(successMessage: string): void {
    // init() resolved the module before setting this.messaging, so this is the
    // cached promise, not a second download.
    this.loadFirebase()
      .then(fb => fb.messaging.getToken(this.messaging, { vapidKey: this.config?.vapid }))
      .then((currentToken) => {
        if (currentToken) {
          this.subscribeNotifications(currentToken)
            .then((success) => {
              if (success) {
                this.tostrService.success("", successMessage);
              } else {
                this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
              }
            })
            .catch(() => {
              this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
            });
        } else {
          this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
        }
      })
      .catch(() => {
        this.tostrService.danger("", 'שגיאה בהגדרת התראות!');
      });
  }

  async subscribeNotifications(token: string): Promise<boolean> {
    if (!token) return false;
    const res = await firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slugService.slug}/notifications-subscribe`, { token }));
    return res.success;
  }

}