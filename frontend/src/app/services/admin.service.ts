import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, firstValueFrom, Observable } from 'rxjs';
import { ChatFile, ChatMessage } from './chat.service';
import { ResponseResult } from '../models/response-result.model';
import { Setting } from '../models/setting.model';
import { Reports, Report } from '../models/report.model';
import { Statistics } from '../models/statistics.model';

export interface PrivilegeUser {
  id?: string;
  username: string;
  email: string;
  publicName: string;
  privileges: Record<string, boolean>;
}

export type EditMsg = {
  new?: boolean;
  message: ChatMessage;
}

@Injectable({
  providedIn: 'root'
})
export class AdminService {
  private messageEdit = new BehaviorSubject<EditMsg | undefined>(undefined);
  messageEditObservable = this.messageEdit.asObservable();

  constructor(
    private http: HttpClient,
  ) { }

  setEditMessage(edit: EditMsg | undefined) {
    this.messageEdit.next(edit);
  }

  getEditMessage(): EditMsg | undefined {
    return this.messageEdit.value;
  }

  getStatistics(): Promise<Statistics> {
    return firstValueFrom(this.http.get<Statistics>('/api/admin/statistics'));
  }

  addMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>('/api/admin/new', message);
  }

  editMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/admin/edit-message`, message);
  }

  deleteMessage(id: number | undefined): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/admin/delete-message/${id}` ,null);
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

  setEmojis(emojis: string[] | undefined) {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/set-emojis', { emojis }));
  }

  getSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>('/api/admin/settings/get'));
  }

  setSettings(settings: Setting[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/settings/set', settings));
  }

  getReports(status: string): Promise<Reports> {
    return firstValueFrom(this.http.get<Reports>('/api/admin/reports/get', {
      params: {
        status: status
      }
    }));
  }

  setReports(report: Report): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>('/api/admin/reports/set', report));
  }
}
