import { ApplicationConfig, importProvidersFrom, provideZoneChangeDetection } from '@angular/core';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import { routes } from './app.routes';
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
import { NbEvaIconsModule } from "@nebular/eva-icons";
import { provideMarkdown } from "ngx-markdown";
import { MarkdownConfig } from "./markdown.config";
import { NgIconsModule, provideIcons } from "@ng-icons/core"; // Import NgIconsModule and provideIcons
import {
  heroBold,
  heroCheck,
  heroCodeBracket,
  heroItalic,
  heroLockClosed,
  heroLockOpen,
  heroPaperAirplane,
  heroPaperClip,
  heroQuestionMarkCircle,
  heroUnderline,
  heroXMark
} from "@ng-icons/heroicons/outline";
import { provideCharts, withDefaultRegisterables } from 'ng2-charts';

export const appConfig: ApplicationConfig = {
  providers: [
    provideZoneChangeDetection({ eventCoalescing: true }),
    provideRouter(routes), // withHashLocation()
    provideHttpClient(),
    provideAnimationsAsync(),
    provideMarkdown(MarkdownConfig),
    provideIcons({
      heroBold,
      heroItalic,
      heroUnderline,
      heroCodeBracket,
      heroPaperClip,
      heroQuestionMarkCircle,
      heroPaperAirplane,
      heroCheck,
      heroXMark,
      heroLockClosed,
      heroLockOpen
    }),
    importProvidersFrom(
      NbThemeModule.forRoot({ name: 'custom' }, undefined, undefined, NbLayoutDirection.RTL),
      NbIconModule,
      NbEvaIconsModule,
      NbMenuModule.forRoot(),
      NbDialogModule.forRoot(),
      NbToastrModule.forRoot({ position: NbGlobalLogicalPosition.TOP_START }),
      NgIconsModule,
      NbSidebarModule.forRoot(),
      NbDatepickerModule.forRoot(),
      NbTimepickerModule.forRoot()
    ),
    provideCharts(
      withDefaultRegisterables()
    )
  ]
};
