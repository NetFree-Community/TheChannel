import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { NbToastrService } from '@nebular/theme';
import { firstValueFrom } from 'rxjs';
import { FirebaseApp, FirebaseOptions, initializeApp } from 'firebase/app';
import { getMessaging, onMessage, getToken } from 'firebase/messaging';
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

    await firstValueFrom(this.http.get<NotificationsConfig>(`/api/channel/${this.slugService.slug}/notifications-config`))
      .then((config) => {
        this.config = config;
      });

    if (!this.config) return;

    if (this.config.enableNotifications) {
      if (!this.config.firebaseConfig) return;

      this.app = initializeApp(this.config.firebaseConfig);
      this.messaging = getMessaging(this.app);
      this.initialized = true;

      onMessage(this.messaging, (payload) => {
        //this.tostrService.success("", 'התראה חדשה!');
      });
      return;
    }
    return;
  }


  async requestPermission() {

    if (Notification.permission === 'granted') {
      this.tostrService.success("", 'כבר אישרתם קבלת התראות!');
      return;
    }

    Notification.requestPermission()
      .then((permission) => {
        if (permission === 'granted') {
          getToken(this.messaging, {
            vapidKey: this.config?.vapid,
          })
            .then((currentToken) => {
              if (currentToken) {
                this.subscribeNotifications(currentToken)
                  .then((success) => {
                    if (success) {
                      this.tostrService.success("", 'התראות הופעלו בהצלחה!');
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
      });
  }

  async subscribeNotifications(token: string): Promise<boolean> {
    if (!token) return false;
    const res = await firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slugService.slug}/notifications-subscribe`, { token }));
    return res.success;
  }

}