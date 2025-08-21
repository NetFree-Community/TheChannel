import { Injectable } from '@angular/core';
import { BehaviorSubject } from 'rxjs';

@Injectable({ providedIn: 'root' })
export class ScrollStateService {
  private isScrolling = new BehaviorSubject<boolean>(false);
  isScrollingObservable = this.isScrolling.asObservable();
  private scrollIdleTimer: ReturnType<typeof setTimeout> | undefined = undefined;

  setScrolling(scrollDelayMs: number = 150) {
    if (!this.isScrolling.value) {
      this.isScrolling.next(true);
    }
    if (this.scrollIdleTimer) {
      clearTimeout(this.scrollIdleTimer);
    }
    this.scrollIdleTimer = setTimeout(() => {
      this.isScrolling.next(false);
    }, scrollDelayMs);
  }

  forceStop() {
    clearTimeout(this.scrollIdleTimer);
    if (this.isScrolling.value) this.isScrolling.next(false);
  }
}