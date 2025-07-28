import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom, Observable } from 'rxjs';
import { ChatFile, ChatMessage } from './chat.service';
import { ResponseResult } from '../models/response-result.model';

export interface PrivilegeUser {
  id?: string;
  username: string;
  email: string;
  publicName: string;
  privileges: Record<string, boolean>;
}

export interface Users {
  id: string;
  username: string;
  email: string;
  publicName: string;
  privileges: Record<string, boolean>;
  blocked: boolean;
  isAdmin: boolean;
}

export interface Setting {
  key: string
  value: string
}

@Injectable({
  providedIn: 'root'
})
export class AdminService {

  constructor(
    private http: HttpClient,
  ) { }

  async getUsersAmount(): Promise<number> {
    try {
      const response = await firstValueFrom(this.http.get<{ amount: number }>('/api/admin/users-amount'));
      return response.amount;
    } catch (error) {
      throw error;
    }
  }

  addMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>('/api/admin/new', message);
  }

  editMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/admin/edit-message`, message);
  }

  deleteMessage(id: number | undefined): Observable<ChatMessage> {
    return this.http.get<ChatMessage>(`/api/admin/delete-message/${id}`);
  }

  uploadFile(formData: FormData) {
    return this.http.post<ChatFile>('/api/admin/upload', formData, {
      reportProgress: true,
      observe: 'events',
      responseType: 'json'
    });
  }

  getPrivilegeUsersList(): Promise<PrivilegeUser[]> {
    return firstValueFrom(this.http.get<PrivilegeUser[]>('/api/admin/privilegs-users/get-list'));
  }

  setPrivilegeUsers(privilegeUsers: PrivilegeUser[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/privilegs-users/set', { list: privilegeUsers }));
  }


  async getAllUsers(): Promise<Users[]> {
    try {
      return await firstValueFrom(
        this.http.get<Users[]>('/api/admin/users/get-list')
      );
    } catch (error) {
      throw error;
    }
  }

  async getBlockedUsers(): Promise<Users[]> {
    try {
      return await firstValueFrom(
        this.http.get<Users[]>('/api/admin/blocked-users')
      );
    } catch (error) {
      throw error;
    }
  }

  async blockUser(email: string): Promise<ResponseResult> {
    try {
      return await firstValueFrom(
        this.http.post<ResponseResult>('/api/admin/block-user', { email })
      );
    } catch (error) {
      throw error;
    }
  }

  async unblockUser(email: string): Promise<ResponseResult> {
    try {
      return await firstValueFrom(
        this.http.post<ResponseResult>('/api/admin/unblock-user', { email })
      );
    } catch (error) {
      throw error;
    }
  }

  async getUserBlockStatus(email: string): Promise<{ email: string, blocked: boolean, isAdmin: boolean }> {
    try {
      return await firstValueFrom(
        this.http.get<{ email: string, blocked: boolean, isAdmin: boolean }>(
          `/api/admin/user-block-status?email=${encodeURIComponent(email)}`
        )
      );
    } catch (error) {
      throw error;
    }
  }



  setEmojis(emojis: string[] | undefined) {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/set-emojis', { emojis }));
  }

  getSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>('/api/admin/settings/get'));
  }

  setSettings(settings: Setting[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/settings/set', settings));
  }
}
