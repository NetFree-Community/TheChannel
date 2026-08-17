import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class ChannelRequestService {

  constructor(private http: HttpClient) {}

  submitRequest(name: string, email: string, desiredSlug: string, description: string): Promise<{ status: string; id: string }> {
    return firstValueFrom(
      this.http.post<{ status: string; id: string }>('/api/channel-request', { name, email, desiredSlug, description })
    );
  }

  getChannelRequests(): Promise<any[]> {
    return firstValueFrom(this.http.get<any[]>('/api/super-admin/channel-requests'));
  }

  approveChannelRequest(id: string, slug: string, notes: string): Promise<{ channelSlug: string; ownerEmail: string }> {
    return firstValueFrom(
      this.http.post<{ channelSlug: string; ownerEmail: string }>(
        `/api/super-admin/channel-requests/${id}/approve`,
        { slug, notes }
      )
    );
  }

  rejectChannelRequest(id: string, notes: string): Promise<void> {
    return firstValueFrom(
      this.http.post<void>(`/api/super-admin/channel-requests/${id}/reject`, { notes })
    );
  }
}
