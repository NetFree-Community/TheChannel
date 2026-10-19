import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { SlugService } from './slug.service';

export interface Ad {
  src: string;
  width: number; // Width in pixels
  // True when the super admin locked the iframe ad (globally or for this
  // channel): src/width are then the global ones and the channel's own
  // ad-iframe-* settings are ignored. Optional — an older backend omits it.
  locked?: boolean;
}

@Injectable({
  providedIn: 'root'
})
export class AdsService {

  constructor(
    private http: HttpClient,
    private slugService: SlugService,
  ) { }

  async getAds(): Promise<Ad> {
    return firstValueFrom(this.http.get<Ad>(`/api/channel/${this.slugService.slug}/ads/settings`));
  }
}
