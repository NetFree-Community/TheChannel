import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, firstValueFrom, Observable, Subject } from 'rxjs';
import { ChatFile, ChatMessage } from './chat.service';
import { ResponseResult } from '../models/response-result.model';
import { Setting } from '../models/setting.model';
import { Reports, Report } from '../models/report.model';
import { Statistics } from '../models/statistics.model';
import { SlugService } from './slug.service';
import { ChannelUser } from '../models/channel-user.model';

export type { ChannelUser };

export type EditMsg = {
  new?: boolean;
  isScheduling?: boolean;
  message: ChatMessage;
  /**
   * Text to put in front of what is typed in the open editor, leaving
   * `message` untouched. A quote used to be written into the edit target's
   * own text, and a scheduled entry is found again on the server by that
   * text — so the save failed once it carried the quote.
   */
  prepend?: string;
}

@Injectable({
  providedIn: 'root'
})
export class AdminService {
  private messageEdit = new BehaviorSubject<EditMsg | undefined>(undefined);
  messageEditObservable = this.messageEdit.asObservable();

  private schedulingBus = new Subject<void>();
  schedulingBusObservable = this.schedulingBus.asObservable();

  private schedulingMessages: ChatMessage[] | null = null;

  constructor(
    private http: HttpClient,
    private slugService: SlugService,
  ) { }

  private get slug() { return this.slugService.slug; }

  clearCache() {
    this.schedulingMessages = null;
  }

  reloadSchedulingMessage() {
    this.schedulingBus.next();
  }

  setEditMessage(edit: EditMsg | undefined) {
    this.messageEdit.next(edit);
  }

  getEditMessage(): EditMsg | undefined {
    return this.messageEdit.value;
  }

  getStatistics(): Promise<Statistics> {
    return firstValueFrom(this.http.get<Statistics>(`/api/channel/${this.slug}/admin/statistics`));
  }

  addMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/channel/${this.slug}/admin/new`, message);
  }

  // Both endpoints answer with the {success} envelope, not the message itself.
  editMessage(message: ChatMessage): Observable<ResponseResult> {
    return this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/edit-message`, message);
  }

  deleteMessage(id: number | undefined): Observable<ResponseResult> {
    return this.http.delete<ResponseResult>(`/api/channel/${this.slug}/admin/delete-message/${id}`);
  }

  uploadFile(formData: FormData) {
    return this.http.post<ChatFile>(`/api/channel/${this.slug}/admin/upload`, formData, {
      reportProgress: true,
      observe: 'events',
      responseType: 'json'
    });
  }

  getChannelUsers(): Promise<ChannelUser[]> {
    return firstValueFrom(this.http.get<ChannelUser[]>(`/api/channel/${this.slug}/admin/users/get`));
  }

  setChannelUsers(users: ChannelUser[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/users/set`, { users }));
  }

  setEmojis(emojis: string[] | undefined) {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/set-emojis`, { emojis }));
  }

  getSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>(`/api/channel/${this.slug}/admin/settings/get`));
  }

  setSettings(settings: Setting[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/settings/set`, settings));
  }

  getReports(status: string): Promise<Reports> {
    return firstValueFrom(this.http.get<Reports>(`/api/channel/${this.slug}/admin/reports/get`, {
      params: {
        status: status
      }
    }));
  }

  setReports(report: Report): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/reports/set`, report));
  }

  private fetchScheduledMessages(): Promise<ChatMessage[]> {
    return firstValueFrom(this.http.get<ChatMessage[]>(`/api/channel/${this.slug}/admin/scheduled-messages/get`));
  }

  async getScheduledMessages(reload?: boolean): Promise<ChatMessage[]> {
    if (this.schedulingMessages && !reload) {
      return this.schedulingMessages;
    }

    try {
      this.schedulingMessages = await this.fetchScheduledMessages();
      return this.schedulingMessages;
    } catch {
      return this.schedulingMessages || [];
    }
  }

  async setScheduledMessage(message: ChatMessage): Promise<ResponseResult> {
    // The update endpoint replaces the whole list, so it is built on the
    // server's current copy, never on the cache. The cache is refreshed only
    // by the dispatch event, and a tab that missed it (an SSE gap, or the
    // event skipped while hasNewMessages is set) re-posted the already
    // published entry with its past timestamp — and the dispatcher posted it
    // a second time. A failed load rejects instead of saving, as before an
    // empty/null cache posted an empty body (400) or wiped the list.
    const list = await this.fetchScheduledMessages();
    this.schedulingMessages = list;
    return this.commitSchedulingMessages([message, ...list]);
  }

  /**
   * `original` is the entry object the feed displayed (the one the editor was
   * opened on); `updated` is the copy to store in its place.
   */
  async editScheduledMessage(original: ChatMessage | undefined, updated: ChatMessage): Promise<ResponseResult> {
    const { list, at } = await this.locateScheduled(original);
    if (at < 0) return Promise.reject('Scheduled message is no longer in the list');
    const next = list.slice();
    next[at] = updated;
    return this.commitSchedulingMessages(next);
  }

  async deleteScheduledMessage(original: ChatMessage | undefined): Promise<ResponseResult> {
    const { list, at } = await this.locateScheduled(original);
    if (at < 0) return Promise.reject('Scheduled message is no longer in the list');
    return this.commitSchedulingMessages(list.filter((_, i) => i !== at));
  }

  /**
   * Re-reads the list from the server and finds the entry the caller holds —
   * the feed's own object — by its time and text. The key used to be the
   * entry's index in the CACHED list, but the cache is replaced under an open
   * editor (every SSE reconnect reloads it) and after a rejected save while
   * the feed keeps its older array, and once a dispatched entry earlier in the
   * list had shifted the indexes, that index resolved to a neighbour. The
   * object the user actually saw cannot drift that way. -1 means it is no
   * longer scheduled (dispatched, or removed by another writer); the feed is
   * then refreshed from the fresh list so the stale row disappears.
   */
  private async locateScheduled(wanted: ChatMessage | undefined): Promise<{ list: ChatMessage[]; at: number }> {
    const list = await this.fetchScheduledMessages();
    this.schedulingMessages = list;
    if (!wanted) return { list, at: -1 };
    // The timestamp is a string off the wire but may be a Date on a client
    // copy kept after a failed re-read, so both are compared as epoch millis
    // (NaN never equals itself, hence the fallback for an unparsable value).
    const when = (m: ChatMessage) => new Date(m.timestamp as any).getTime() || 0;
    const at = list.findIndex(m => when(m) === when(wanted) && m.text === wanted.text);
    if (at < 0) this.reloadSchedulingMessage();
    return { list, at };
  }

  /**
   * Copy, post, then commit. The list is shared with the feed's scheduled
   * section, so mutating it before the POST left every viewer of it with a
   * list the server had rejected. Callers refresh the feed through
   * reloadSchedulingMessage() once this resolves.
   */
  private async commitSchedulingMessages(next: ChatMessage[]): Promise<ResponseResult> {
    let res: ResponseResult;
    try {
      res = await firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/scheduled-messages/update`, next));
    } catch (e) {
      // Any non-2xx — including the 503 the server answers while the
      // dispatcher holds the list's lock — leaves the server's list as it
      // was, but the cache was already replaced by the fresh read above while
      // the feed still shows its older array. Both are realigned here, so a
      // retry acts on what is on screen and no stale row lingers.
      this.schedulingMessages = await this.fetchScheduledMessages().catch(() => this.schedulingMessages);
      this.reloadSchedulingMessage();
      throw e;
    }
    // The server stamps fields the client does not know (the scheduler's
    // name), so the cache is re-read rather than kept as the client copy.
    this.schedulingMessages = await this.fetchScheduledMessages().catch(() => next);
    return res;
  }
}
