import { Component, ElementRef, OnDestroy, OnInit, Renderer2, RendererStyleFlags2, ViewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AdvertisingComponent } from "./advertising/advertising.component";

import { Ad, AdsService } from '../../services/ads.service';
import {
  NbButtonModule,
  NbCardModule,
  NbIconModule,
  NbInputModule,
  NbLayoutModule,
  NbListModule,
  NbMenuModule,
  NbSidebarModule,
  NbSpinnerModule,
  NbToastrService,
} from "@nebular/theme";
import { InputFormComponent } from "./chat/input-form/input-form.component";
import { AuthService } from "../../services/auth.service";
import { ChannelHeaderComponent } from "./channel-header/channel-header.component";
import { ChatComponent } from "./chat/chat.component";
import { User } from '../../models/user.model';
import { ActivatedRoute, Router } from '@angular/router';
import { SlugService } from '../../services/slug.service';
import { AdminService } from '../../services/admin.service';
import { ChatService } from '../../services/chat.service';
import { MagnetAdsService } from '../../services/magnet-ads.service';
import { ChannelRequestService } from '../../services/channel-request.service';
import { NotificationsService } from '../../services/notifications.service';
import { Subscription } from 'rxjs';

@Component({
  selector: 'app-channel',
  imports: [
    FormsModule,
    AdvertisingComponent,
    NbLayoutModule,
    NbCardModule,
    NbInputModule,
    NbSpinnerModule,
    InputFormComponent,
    ChannelHeaderComponent,
    NbButtonModule,
    NbIconModule,
    NbMenuModule,
    NbSidebarModule,
    NbListModule,
    ChatComponent
  ],
  templateUrl: './channel.component.html',
  styleUrl: './channel.component.scss'
})
export class ChannelComponent implements OnInit, OnDestroy {

  @ViewChild('inputForm', { static: false })
  set inputForm(element: ElementRef) {
    if (element) {
      setTimeout(() => {
        this.updateInputBottomOffset();
      }, 0);
    }
  }

  constructor(
    private adsService: AdsService,
    private _authService: AuthService,
    private renderer: Renderer2,
    private el: ElementRef,
    private route: ActivatedRoute,
    private router: Router,
    private slugService: SlugService,
    private adminService: AdminService,
    private chatService: ChatService,
    private magnetAds: MagnetAdsService,
    private channelRequestService: ChannelRequestService,
    private notificationsService: NotificationsService,
    private toastr: NbToastrService,
  ) { }

  ad: Ad = { src: '', width: 0 };
  userInfo?: User;
  slugReady = false;
  noChannel = false;
  private paramSub?: Subscription;

  // Channel request form state
  reqName = '';
  reqEmail = '';
  reqSlug = '';
  reqDescription = '';
  reqSubmitting = false;
  reqSubmitted = false;

  ngOnInit(): void {
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('slug');
      this.initChannel(slug);
    });
  }

  ngOnDestroy(): void {
    this.paramSub?.unsubscribe();
  }

  private async initChannel(slug: string | null): Promise<void> {
    this.slugReady = false;
    this.noChannel = false;
    // Yield to Angular's change detection so the @if (slugReady) block
    // actually destroys ChatComponent before we reinitialise with the new slug.
    // Without this, false→true in the same synchronous frame is collapsed and
    // the child is never torn down, leaving stale state (isVisible, messages, etc).
    await Promise.resolve();

    if (!slug) {
      const user = await this._authService.loadUserInfo();
      const roles = user?.channelRoles;
      const firstSlug = roles ? Object.keys(roles)[0] : '';
      if (firstSlug) {
        this.router.navigate(['/channel', firstSlug], { replaceUrl: true });
      } else {
        this.userInfo = user ?? undefined;
        this.reqName = user?.publicName || '';
        this.reqEmail = user?.email || '';
        this.noChannel = true;
      }
      return;
    }

    this.slugService.slug = slug;
    this.chatService.clearCache();
    this.adminService.clearCache();
    this.notificationsService.reset();
    // Drop any in-progress edit/compose state from the previous channel, otherwise
    // the replayed BehaviorSubject value makes the input form post the old message
    // id into the new channel.
    this.adminService.setEditMessage(undefined);
    this.magnetAds.clearCache();
    this.slugReady = true;

    this.adsService.getAds().then(ad => {
      this.ad = ad;
    });
    this._authService.loadUserInfo().then(res => {
      this.userInfo = res;
    }).catch(() => {
      // Anonymous visitor on a public channel — read-only view.
      this.userInfo = undefined;
    });
  }

  async submitChannelRequest(): Promise<void> {
    if (!this.reqName || !this.reqEmail || !this.reqSlug || !this.reqDescription) {
      this.toastr.warning('', 'יש למלא את כל השדות');
      return;
    }
    this.reqSubmitting = true;
    try {
      await this.channelRequestService.submitRequest(this.reqName, this.reqEmail, this.reqSlug, this.reqDescription);
      this.reqSubmitted = true;
    } catch {
      this.toastr.danger('', 'שגיאה בשליחת הבקשה, נסה שוב');
    } finally {
      this.reqSubmitting = false;
    }
  }

  async logout(): Promise<void> {
    if (await this._authService.logout()) {
      this.router.navigate(['/login']);
    } else {
      this.toastr.danger('', 'שגיאה בהתנתקות');
    }
  }

  // A role on some other channel must not open the composer here — gate on the
  // role the user holds on the channel currently being viewed.
  hasAnyRole(user: User | undefined): boolean {
    const role = user?.channelRoles?.[this.slugService.slug];
    return role === 'owner' || role === 'moderator' || role === 'writer';
  }

  onInputHeightChanged() {
    this.updateInputBottomOffset();
  }

  updateInputBottomOffset() {
    let inputForm = document.getElementById('inputForm');
    let h = inputForm?.clientHeight;
    this.renderer.setStyle(this.el.nativeElement, '--input-height', `${h}px`, RendererStyleFlags2.DashCase);
  }
}
