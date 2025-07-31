import { Routes } from '@angular/router';
import { LoginComponent } from './components/login/login.component';
import { ChannelComponent } from './components/channel/channel.component';
import { AuthGuard } from "./services/chat-guard.guard";

export const routes: Routes = [
  { path: 'login', component: LoginComponent },
  {
    path: '',
    component: ChannelComponent,
    canActivate: [AuthGuard],
  },
  {
    path: '**',
    redirectTo: ''
  }
];