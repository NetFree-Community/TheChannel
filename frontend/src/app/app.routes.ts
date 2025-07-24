import { Routes } from '@angular/router';
import { LoginComponent } from './components/login/login.component';
import { ChannelComponent } from './components/channel/channel.component';

export const routes: Routes = [
    { path: 'login', component: LoginComponent },
    {
        path: '',
        component: ChannelComponent, //MainComponent,
        // children: [
        //     {
        //         path: 'admin',
        //         loadChildren: () => import('./components/admin/admin.routes').then(m => m.adminRoutes)
        //     },
        //     { path: '', component: ChannelComponent, pathMatch: 'full', canActivate: [AuthGuard] },
        //     { path: '**', redirectTo: '' }
        // ]
    },
];