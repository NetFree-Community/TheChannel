import { Routes } from '@angular/router';
import { LoginComponent } from './components/login/login.component';
import { ChatComponent } from './components/chat/chat.component';
import { AuthGuard } from './services/chat-guard.guard';

export const routes: Routes = [
    { path: 'login', component: LoginComponent},
    { path: '', component: ChatComponent, pathMatch: 'full', canActivate: [AuthGuard] },
    { path: '**', redirectTo: '' }
];
