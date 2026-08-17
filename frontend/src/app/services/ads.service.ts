import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { SlugService } from './slug.service';

export interface Ad {
  src: string;
  width: number; // Width in pixels
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
