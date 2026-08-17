import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom, Observable } from 'rxjs';
import { Channel, ChannelFeatureFlags } from '../models/channel.model';
import { ResponseResult } from '../models/response-result.model';
import { SlugService } from './slug.service';

export type MessageType = 'md' | 'text' | 'image' | 'video' | 'audio' | 'document' | 'other';
export type Reactions = { [key: string]: number }
export interface ChatMessage {
  id?: number;
  type?: MessageType;
  text?: string;
  timestamp?: Date;
  userId?: number | null;
  author?: string;
  authorId?: string;
  last_edit?: Date;
  deleted?: boolean;
  file?: ChatFile;
  views?: number;
  reactions?: Reactions;
  is_ads?: boolean;
}
export type ChatResponse = ChatMessage[];

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
  private channelInfoRequest?: Promise<void>;

  constructor(private http: HttpClient, private slugService: SlugService) { }

  private get slug() { return this.slugService.slug; }

  updateChannelInfo(): Promise<void> {
    // Deduplicated: several components need the channel info (and the feature
    // flags it carries) on the same first paint.
    this.channelInfoRequest ??= firstValueFrom(this.http.get<Channel>(`/api/channel/${this.slug}/info`))
      .then(info => { this.channelInfo = info; })
      .finally(() => { this.channelInfoRequest = undefined; });
    return this.channelInfoRequest;
  }

  // Resolves once the feature flags are known, without issuing a second request
  // when the header already has one in flight.
  ensureChannelInfo(): Promise<void> {
    return this.channelInfo ? Promise.resolve() : this.updateChannelInfo();
  }

  // A missing features object means an older backend or a response cached before
  // the flags existed — treat it as "everything enabled" so the UI is never
  // stripped by an answer that simply does not mention the feature.
  isFeatureEnabled(feature: keyof ChannelFeatureFlags): boolean {
    return this.channelInfo?.features?.[feature] !== false;
  }

  get reactionsEnabled(): boolean { return this.isFeatureEnabled('reactions'); }
  get fileUploadsEnabled(): boolean { return this.isFeatureEnabled('fileUploads'); }
  get reportsEnabled(): boolean { return this.isFeatureEnabled('reports'); }
  get scheduledMessagesEnabled(): boolean { return this.isFeatureEnabled('scheduledMessages'); }

  editChannelInfo(name: string, description: string, logoUrl: string): Observable<ResponseResult> {
    return this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/edit-channel-info`, { name, description, logoUrl });
  }

  getMessages(offset: number, limit: number, direction: string): Observable<ChatResponse> {
    return this.http.get<ChatResponse>(`/api/channel/${this.slug}/messages`, {
      params: {
        offset: offset.toString(),
        limit: limit.toString(),
        direction: direction
      }
    });
  }

  setReact(messageId: number, react: string) {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/reactions/set-reactions`, { messageId, emoji: react }));
  }

  clearCache() {
    this.emojis = [];
    this.channelInfo = undefined;
    this.channelInfoRequest = undefined;
  }

  async getEmojisList(reload: boolean = false): Promise<string[]> {
    if (this.emojis.length && !reload) return this.emojis;
    this.emojis = await firstValueFrom(this.http.get<string[]>(`/api/channel/${this.slug}/emojis/list`));
    return this.emojis;
  }

  reportMessage(messageId: number, reason: string): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/messages/report`, { messageId, reason }));
  }

  sseListener(): EventSource {
    if (this.eventSource) {
      this.eventSource.close();
    }

    this.eventSource = new EventSource(`/api/channel/${this.slug}/events`);

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
}
