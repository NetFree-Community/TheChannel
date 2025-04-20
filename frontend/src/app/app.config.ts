import { ApplicationConfig, importProvidersFrom, provideZoneChangeDetection } from '@angular/core';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';

import { routes } from './app.routes';
import {
  NbDialogModule,
  NbGlobalLogicalPosition,
  NbIconModule,
  NbLayoutDirection,
  NbMenuModule,
  NbThemeModule,
  NbToastrModule
} from "@nebular/theme";
import { provideAnimationsAsync } from "@angular/platform-browser/animations/async";
import { NbEvaIconsModule } from "@nebular/eva-icons";
import { provideMarkdown } from "ngx-markdown";
import { MarkdownConfig } from "./markdown.config";
import { NgIconsModule, provideIcons } from "@ng-icons/core"; // Import NgIconsModule and provideIcons
import { heroBold, heroItalic, heroUnderline, heroCodeBracket, heroPaperClip, heroQuestionMarkCircle } from "@ng-icons/heroicons/outline";

export const appConfig: ApplicationConfig = {
  providers: [
    provideZoneChangeDetection({ eventCoalescing: true }),
    provideRouter(routes),
    provideHttpClient(),
    provideAnimationsAsync(),
    provideMarkdown(MarkdownConfig),
    provideIcons({ heroBold, heroItalic, heroUnderline, heroCodeBracket, heroPaperClip, heroQuestionMarkCircle }),
    importProvidersFrom(
      NbThemeModule.forRoot(undefined, undefined, undefined, NbLayoutDirection.RTL),
      NbIconModule,
      NbEvaIconsModule,
      NbMenuModule.forRoot(),
      NbDialogModule.forRoot(),
      NbToastrModule.forRoot({ position: NbGlobalLogicalPosition.TOP_START }),
      NgIconsModule
    )
  ]
};
