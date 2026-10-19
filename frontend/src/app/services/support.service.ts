import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

export type SupportStatus = 'open' | 'answered' | 'closed';

export interface SupportMessage {
  author: 'user' | 'admin';
  authorName: string;
  body: string;
  createdAt: string;
  /** 'email' when it arrived as a mail to the channels mailbox. */
  source?: 'email';
}

export interface SupportTicket {
  id: string;
  subject: string;
  name: string;
  email: string;
  channelSlug?: string;
  authenticated: boolean;
  status: SupportStatus;
  messages: SupportMessage[];
  createdAt: string;
  updatedAt: string;
  /** 'email' when the ticket was opened by a mail to the channels mailbox. */
  source?: 'email';
}

export interface CreatedTicket {
  id: string;
  accessToken: string;
}

/**
 * Local record of a ticket opened without a session. The server hands the
 * access token back exactly once, at creation, so an anonymous sender who
 * loses it has no way to read the reply — it is kept here so the thread
 * survives a page reload on the same browser.
 *
 * Signed-in users need none of this: their tickets are found by session email
 * through /api/support/my-tickets.
 */
const ANON_TICKETS_KEY = 'supportTickets';

interface StoredTicket {
  id: string;
  token: string;
  subject: string;
  createdAt: string;
}

@Injectable({ providedIn: 'root' })
export class SupportService {
  constructor(private http: HttpClient) {}

  /**
   * Opens a ticket. Works with or without a session: when there is one the
   * server takes the name and email from it and ignores whatever is passed
   * here, so a ticket can never be attributed to someone else.
   */
  async createTicket(input: {
    subject: string;
    body: string;
    name?: string;
    email?: string;
    channelSlug?: string;
  }): Promise<CreatedTicket> {
    const created = await firstValueFrom(
      this.http.post<CreatedTicket>('/api/support/tickets', input),
    );
    if (created?.accessToken) {
      this.rememberAnonTicket({
        id: created.id,
        token: created.accessToken,
        subject: input.subject,
        createdAt: new Date().toISOString(),
      });
    }
    return created;
  }

  /** A signed-in user's own threads. */
  myTickets(): Promise<SupportTicket[]> {
    return firstValueFrom(this.http.get<SupportTicket[]>('/api/support/my-tickets'));
  }

  getTicket(id: string, token?: string): Promise<SupportTicket> {
    return firstValueFrom(
      this.http.get<SupportTicket>(`/api/support/tickets/${id}`, {
        headers: this.tokenHeaders(token),
      }),
    );
  }

  reply(id: string, body: string, token?: string): Promise<SupportTicket> {
    return firstValueFrom(
      this.http.post<SupportTicket>(`/api/support/tickets/${id}/reply`, { body }, {
        headers: this.tokenHeaders(token),
      }),
    );
  }

  /**
   * The anonymous access token travels in a header. As a query parameter it
   * was written to the server's request log on every read and reply, and it
   * is the only credential on an anonymous thread.
   */
  private tokenHeaders(token?: string): Record<string, string> {
    return token ? { 'X-Ticket-Token': token } : {};
  }

  // --- super admin ---

  adminList(): Promise<SupportTicket[]> {
    return firstValueFrom(this.http.get<SupportTicket[]>('/api/super-admin/support/tickets'));
  }

  adminReply(id: string, body: string): Promise<SupportTicket> {
    return firstValueFrom(
      this.http.post<SupportTicket>(`/api/super-admin/support/tickets/${id}/reply`, { body }),
    );
  }

  adminSetStatus(id: string, status: SupportStatus): Promise<SupportTicket> {
    return firstValueFrom(
      this.http.post<SupportTicket>(`/api/super-admin/support/tickets/${id}/status`, { status }),
    );
  }

  // --- anonymous ticket bookkeeping ---

  /** Tickets opened from this browser without a session, newest first. */
  anonTickets(): StoredTicket[] {
    try {
      const raw = localStorage.getItem(ANON_TICKETS_KEY);
      const list = raw ? JSON.parse(raw) : [];
      return Array.isArray(list) ? list : [];
    } catch {
      // Storage unavailable or holding something that is not our list. Losing
      // the local record is survivable; throwing here is not.
      return [];
    }
  }

  private rememberAnonTicket(entry: StoredTicket): void {
    try {
      // Bounded: this is a convenience record, not an archive.
      const list = [entry, ...this.anonTickets().filter(t => t.id !== entry.id)].slice(0, 20);
      localStorage.setItem(ANON_TICKETS_KEY, JSON.stringify(list));
    } catch {
      // Private mode or a full quota. The ticket still exists server-side; the
      // sender simply cannot re-open it from this browser.
    }
  }
}
