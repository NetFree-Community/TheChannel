import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { NbToastrService } from '@nebular/theme';
import { firstValueFrom } from 'rxjs';
import { FirebaseApp, FirebaseOptions, initializeApp } from 'firebase/app';
import { getMessaging, onMessage, getToken } from 'firebase/messaging';


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
  ) { }

  async init() {
    if (this.initialized) return;

    await firstValueFrom(this.http.get<NotificationsConfig>('/api/channel/notifications-config'))
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
        this.tostrService.success("", 'התראה חדשה!');
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
    return firstValueFrom(this.http.post<boolean>('/api/channel/notifications-subscribe', { token }))
  }

}