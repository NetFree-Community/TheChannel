import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { BehaviorSubject, firstValueFrom, Observable, Subject } from 'rxjs';
import { Channel } from '../models/channel.model';
import { ResponseResult } from '../models/response-result.model';

export type MessageType = 'md' | 'text' | 'image' | 'video' | 'audio' | 'document' | 'other';
export type Reactions = { [key: string]: number }
export interface ChatMessage {
  id?: number;
  type?: MessageType;
  text?: string;
  timestamp?: Date;
  userId?: number | null;
  author?: string;
  last_edit?: Date;
  deleted?: boolean;
  file?: ChatFile;
  views?: number;
  reactions?: Reactions;
}
export interface ChatResponse {
  messages: ChatMessage[];
  hasMore: boolean;
}

export interface ChatFile {
  url: string;
  filename: string;
  filetype: string;
}

export interface Attachment {
  file: File;
  url?: string;
  uploadProgress?: number;
  uploading?: boolean;
  embedded?: string;
}

@Injectable({
  providedIn: 'root'
})
export class ChatService {
  private eventSource!: EventSource;
  private emojis: string[] = [];
  public channelInfo?: Channel;

  private messageEdit = new BehaviorSubject<ChatMessage | undefined>(undefined);
  messageEditObservable = this.messageEdit.asObservable();

  constructor(private http: HttpClient) { }

  async updateChannelInfo() {
    this.channelInfo = await firstValueFrom(this.http.get<Channel>('/api/channel/info'));
    return;
  }

  editChannelInfo(name: string, description: string, logoUrl: string): Observable<ResponseResult> {
    return this.http.post<ResponseResult>('/api/admin/edit-channel-info', { name, description, logoUrl });
  }

  getMessages(offset: number, limit: number): Observable<ChatResponse> {
    return this.http.get<ChatResponse>('/api/messages', {
      params: {
        offset: offset.toString(),
        limit: limit.toString()
      }
    });
  }

  setReact(messageId: number, react: string) {
    return firstValueFrom(this.http.post<ResponseResult>('/api/reactions/set-reactions', { messageId, emoji: react }));
  }

  async getEmojisList(reload: boolean = false): Promise<string[]> {
    if (this.emojis && !reload) return Promise.resolve(this.emojis); // הסרתי את בדיקת האורך, משום שזה יוצר קריאות מיותרות לשרת כאשר לא מוגדר אימוגים
    this.emojis = await firstValueFrom(this.http.get<string[]>('/api/emojis/list'));
    return this.emojis;
  }

  sseListener(): EventSource {
    if (this.eventSource) {
      this.eventSource.close();
    }

    this.eventSource = new EventSource('/api/events');

    this.eventSource.onopen = () => {
      console.log('Connection opened');
    };

    this.eventSource.onerror = (error) => {
      console.error('EventSource failed:', error);
    };

    return this.eventSource;
  }

  sseClose() {
    if (this.eventSource) {
      this.eventSource.close();
    }
  }

  setEditMessage(message?: ChatMessage) {
    this.messageEdit.next(message);
  }
}
