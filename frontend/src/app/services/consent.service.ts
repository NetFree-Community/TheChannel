import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

/** A channel opener's answers; `marketing` is null until it was asked. */
export interface OwnerConsentState {
  terms_accepted: boolean;
  marketing: boolean | null;
}

/**
 * Terms, privacy and mailing-list answers of someone opening a channel
 * (backend/owner_consent.go). Only the create flow asks; readers never do.
 */
@Injectable({ providedIn: 'root' })
export class ConsentService {
  constructor(private http: HttpClient) {}

  get(): Promise<OwnerConsentState> {
    return firstValueFrom(this.http.get<OwnerConsentState>('/api/me/consent'));
  }

  accept(marketing: boolean): Promise<OwnerConsentState> {
    return firstValueFrom(
      this.http.post<OwnerConsentState>('/api/me/consent', { accept_terms: true, marketing }),
    );
  }
}
