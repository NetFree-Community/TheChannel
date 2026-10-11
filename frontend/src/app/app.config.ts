import { ApplicationConfig, importProvidersFrom, provideZoneChangeDetection } from '@angular/core';
import { provideRouter } from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { routes } from './app.routes';
import { channelDisabledInterceptor } from './interceptors/channel-disabled.interceptor';
import { sessionExpiredInterceptor } from './interceptors/session-expired.interceptor';
import {
  NbDatepickerModule,
  NbDialogModule,
  NbGlobalLogicalPosition,
  NbIconModule,
  NbLayoutDirection,
  NbMenuModule,
  NbSidebarModule,
  NbThemeModule,
  NbTimepickerModule,
  NbToastrModule
} from "@nebular/theme";
import { provideAnimationsAsync } from "@angular/platform-browser/animations/async";
import { provideEvaIconSubset } from './eva-icons.provider';
import { provideMarkdown } from "ngx-markdown";
import { MarkdownConfig } from "./markdown.config";
import { NgIconsModule, provideIcons } from "@ng-icons/core"; // Import NgIconsModule and provideIcons
import { heroBold, heroItalic, heroUnderline } from "@ng-icons/heroicons/outline";
import { provideCharts, withDefaultRegisterables } from 'ng2-charts';

export const appConfig: ApplicationConfig = {
  providers: [
    // Only the Eva icons in use (scripts/gen-eva-subset.mjs) instead of the
    // whole pack.
    provideEvaIconSubset(),
    provideZoneChangeDetection({ eventCoalescing: true }),
    provideRouter(routes), // withHashLocation()
    provideHttpClient(withInterceptors([channelDisabledInterceptor, sessionExpiredInterceptor])),
    provideAnimationsAsync(),
    provideMarkdown(MarkdownConfig),
    // The composer's three formatting buttons; everything else is Eva.
    provideIcons({ heroBold, heroItalic, heroUnderline }),
    importProvidersFrom(
      NbThemeModule.forRoot({ name: 'custom' }, undefined, undefined, NbLayoutDirection.RTL),
      NbIconModule,
      NbMenuModule.forRoot(),
      NbDialogModule.forRoot(),
      NbToastrModule.forRoot({
        position: NbGlobalLogicalPosition.TOP_START,
        // Nebular's 3 s default vanished while a writer on a phone was still
        // looking at the keyboard; errors are the only feedback channel here,
        // so they stay until read and the same error is not stacked twice.
        duration: 5000,
        destroyByClick: true,
        preventDuplicates: true,
        duplicatesBehaviour: 'previous',
      }),
      NgIconsModule,
      NbSidebarModule.forRoot(),
      NbDatepickerModule.forRoot(),
      NbTimepickerModule.forRoot(),
    ),
    provideCharts(
      withDefaultRegisterables()
    )
  ]
};
