import { Routes } from '@angular/router';
import { ChannelComponent } from './components/channel/channel.component';
import { AuthGuard } from './services/chat-guard.guard';
import { SuperAdminGuard } from './guards/super-admin.guard';
import { ChannelManagerGuard } from './guards/channel-manager.guard';

// Only the channel page itself is in the initial bundle: it is what nearly
// every visitor lands on (a shared link), and its first paint is the one that
// matters. The landing page, login, the manage page and the super-admin panel
// are downloaded when — and only if — someone navigates to them.
const loadManagePage = () =>
  import('./components/manage/manage-channel.component').then(m => m.ManageChannelComponent);

export const routes: Routes = [
  {
    path: '',
    loadComponent: () => import('./components/landing/landing-page.component').then(m => m.LandingPageComponent),
  },
  {
    path: 'login',
    loadComponent: () => import('./components/login/login.component').then(m => m.LoginComponent),
  },
  {
    path: 'channel',
    component: ChannelComponent,
    canActivate: [AuthGuard],
  },
  // The channel's management screen is a page of its own (deep-linkable,
  // back-button friendly, full width on a phone) rather than a dialog over
  // the feed. `tab` is the section id, e.g. /channel/news/manage/users.
  {
    path: 'channel/:slug/manage',
    canActivate: [AuthGuard, ChannelManagerGuard],
    loadComponent: loadManagePage,
  },
  {
    path: 'channel/:slug/manage/:tab',
    canActivate: [AuthGuard, ChannelManagerGuard],
    loadComponent: loadManagePage,
  },
  {
    path: 'channel/:slug',
    component: ChannelComponent,
    canActivate: [AuthGuard],
  },
  {
    path: 'super-admin',
    canActivate: [AuthGuard, SuperAdminGuard],
    loadComponent: () => import('./components/super-admin/super-admin-panel.component').then(m => m.SuperAdminPanelComponent),
  },
  {
    path: '**',
    redirectTo: '',
  },
];
