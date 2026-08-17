import { Routes } from '@angular/router';
import { LoginComponent } from './components/login/login.component';
import { ChannelComponent } from './components/channel/channel.component';
import { AuthGuard } from "./services/chat-guard.guard";
import { SuperAdminGuard } from './guards/super-admin.guard';
import { SuperAdminPanelComponent } from './components/super-admin/super-admin-panel.component';
import { LandingPageComponent } from './components/landing/landing-page.component';

export const routes: Routes = [
  { path: '', component: LandingPageComponent },
  { path: 'login', component: LoginComponent },
  {
    path: 'channel',
    component: ChannelComponent,
    canActivate: [AuthGuard],
  },
  {
    path: 'channel/:slug',
    component: ChannelComponent,
    canActivate: [AuthGuard],
  },
  {
    path: 'super-admin',
    component: SuperAdminPanelComponent,
    canActivate: [AuthGuard, SuperAdminGuard],
  },
  {
    path: '**',
    redirectTo: ''
  }
];
