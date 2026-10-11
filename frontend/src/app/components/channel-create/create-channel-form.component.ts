import {
  AfterViewInit, Component, ElementRef, EventEmitter, Input, OnDestroy, OnInit, Output, ViewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import {
  NbAlertModule,
  NbButtonModule,
  NbCardModule,
  NbDialogService,
  NbFormFieldModule,
  NbIconModule,
  NbInputModule,
} from '@nebular/theme';
import { Subject, Subscription, of } from 'rxjs';
import { catchError, debounceTime, map, switchMap } from 'rxjs/operators';
import {
  CHANNEL_DESCRIPTION_MAX,
  CHANNEL_NAME_MAX,
  ChannelService,
  MAX_CHANNELS_PER_ACCOUNT,
  SLUG_MAX_LENGTH,
  SLUG_MIN_LENGTH,
  SLUG_PATTERN,
  SlugAvailability,
  retryAfterMinutes,
  sanitizeSlugInput,
  slugifyChannelName,
} from '../../services/channel.service';
import { ShareService } from '../../services/share.service';
import { MyChannelsService } from '../../services/my-channels.service';

/** `unknown`: the live check could not run (throttled, offline); the server decides on submit. */
type SlugState = 'empty' | 'checking' | 'available' | 'unavailable' | 'unknown';

/** What the error alert offers besides its sentence. */
type FormErrorKind = '' | 'limit' | 'auth';

/**
 * The channel-opening card: a signed-in user POSTs /api/channels/create and
 * the owner is taken from the session — the only way a channel is opened.
 *
 * The form is one screen: the name (the address is derived from it and shown
 * live underneath, with the availability verdict), an optional description
 * and one button. The address field itself stays hidden until the user asks
 * to change it, because almost nobody needs to.
 *
 * Once the channel exists the card switches to its `created` state: the full
 * channel URL with copy / WhatsApp / share buttons, three next steps, and the
 * button that finally enters the channel. Entering is deliberately a user
 * action rather than an automatic redirect, so there is a moment to share.
 */
@Component({
  selector: 'app-create-channel-form',
  standalone: true,
  imports: [
    FormsModule,
    NbCardModule,
    NbButtonModule,
    NbInputModule,
    NbFormFieldModule,
    NbIconModule,
    NbAlertModule,
  ],
  templateUrl: './create-channel-form.component.html',
  styleUrl: './create-channel-form.component.scss',
})
export class CreateChannelFormComponent implements OnInit, AfterViewInit, OnDestroy {
  @Input() title = 'פתיחת ערוץ';
  @Input() subtitle = '';

  /**
   * Emitted with the new slug when the user asks to enter the channel — the
   * host refreshes the cached user (the new owner role) and navigates.
   */
  @Output() created = new EventEmitter<string>();

  /** The "my channels" link in the channel-limit error; the host shows its list. */
  @Output() backToList = new EventEmitter<void>();

  @ViewChild('nameInput') nameInput?: ElementRef<HTMLInputElement>;
  @ViewChild('slugInput') slugInput?: ElementRef<HTMLInputElement>;

  name = '';
  slug = '';
  description = '';

  readonly nameMax = CHANNEL_NAME_MAX;
  readonly descriptionMax = CHANNEL_DESCRIPTION_MAX;
  readonly slugMin = SLUG_MIN_LENGTH;
  readonly slugMax = SLUG_MAX_LENGTH;
  readonly maxChannels = MAX_CHANNELS_PER_ACCOUNT;

  slugState: SlugState = 'empty';
  slugMessage = '';
  /** The address field is revealed only on request ("לשנות את הכתובת"). */
  slugEditing = false;
  /** A free `slug-N` found after the chosen address turned out taken. */
  suggestedSlug = '';
  suggesting = false;

  submitting = false;
  formError = '';
  formErrorKind: FormErrorKind = '';

  /** Set once the channel exists; switches the card to the success screen. */
  createdSlug = '';
  createdName = '';
  copied = false;
  entering = false;

  /** Host shown in the URL preview, e.g. `example.com/channel/my-slug`. */
  readonly host = window.location.host;

  readonly nextSteps = [
    {
      icon: 'paper-plane-outline',
      title: 'כתבו הודעה ראשונה',
      text: 'תיבת הכתיבה נמצאת בתחתית מסך הערוץ. מה שתכתבו יגיע מיד לכל מי שפתח את הקישור.',
    },
    {
      icon: 'share-outline',
      title: 'שתפו את הקישור',
      text: 'שלחו אותו בוואטסאפ, במייל או בכל מקום — כל מי שיש לו את הקישור רואה את הערוץ. כפתור השיתוף נמצא גם בראש הערוץ.',
    },
    {
      icon: 'people-outline',
      title: 'הוסיפו כותבים ומנהלים',
      text: 'לוחצים על השם שלכם בראש הערוץ ובוחרים "ניהול הערוץ" — שם מוסיפים עוד אנשים שיכתבו וינהלו, ומשנים שם, תיאור ולוגו.',
    },
  ];

  // True once the user edits the slug by hand: from then on the name no longer
  // overwrites it.
  private slugTouched = false;
  private readonly slugChecks$ = new Subject<string>();
  private slugSub?: Subscription;
  private copyResetTimer?: ReturnType<typeof setTimeout>;
  /** Re-runs a live check the server throttled, once its Retry-After is up. */
  private recheckTimer?: ReturnType<typeof setTimeout>;
  // Bumped on every slug change so a suggestion search started for an earlier
  // address cannot surface its answer under a newer one.
  private suggestSeq = 0;

  constructor(
    private channelService: ChannelService,
    private dialogService: NbDialogService,
    private share: ShareService,
    private myChannels: MyChannelsService,
    private router: Router,
  ) {}

  /**
   * The same guide the manage page shows, opened here as a dialog. Imported
   * on click: this form ships with the (eager) channel page, and a static
   * import put the whole guide into every reader's initial bundle.
   */
  async openGuide(): Promise<void> {
    const { GuideComponent } = await import('../admin/guide/guide.component');
    this.dialogService.open(GuideComponent, {
      closeOnBackdropClick: true,
      context: { dialogMode: true },
    });
  }

  /** The link handed to readers — never hardcode the domain. */
  get channelUrl(): string {
    return this.share.channelUrl(this.createdSlug);
  }

  get inviteText(): string {
    return this.share.inviteText(this.createdName, this.channelUrl);
  }

  get whatsappHref(): string {
    return this.share.whatsappUrl(this.inviteText);
  }

  get canShare(): boolean {
    return this.share.canShare;
  }

  ngOnInit(): void {
    // No distinctUntilChanged here: queueSlugCheck() flips the field to
    // 'checking' on every keystroke, and dropping a re-check of the last
    // checked slug (type a letter and backspace it within the debounce) left
    // the spinner on forever. The debounce already coalesces bursts and
    // switchMap cancels a stale request.
    this.slugSub = this.slugChecks$.pipe(
      debounceTime(400),
      switchMap(slug => this.channelService.checkSlugAvailability(slug).pipe(
        map(result => ({ slug, result, status: 200 })),
        // A failed check must not wedge the field in "checking" forever; treat
        // it as "no opinion" and let the server have the last word on submit.
        catchError(err => of({ slug, result: null as SlugAvailability | null, status: Number(err?.status) || 0 })),
      )),
    ).subscribe(({ slug, result, status }) => {
      // The field may have been cleared or made invalid while the request was
      // in flight; that state was set by queueSlugCheck() and must stand.
      if (slug !== this.slug) return;
      if (!result) {
        this.markUnchecked(slug, status);
        this.settleVerdict();
        return;
      }
      this.slugState = result.available ? 'available' : 'unavailable';
      this.slugMessage = result.available ? '' : this.reasonText(result.reason);
      // Offer a way out right away rather than after a failed submit.
      if (!result.available && result.reason === 'taken') this.suggestFreeSlug(slug, 3);
      this.settleVerdict();
    });
  }

  ngAfterViewInit(): void {
    // Only on a wide screen: on a phone a programmatic focus pops the keyboard
    // over the explanation the user has not read yet.
    if (window.matchMedia?.('(min-width: 768px)').matches) {
      setTimeout(() => this.nameInput?.nativeElement.focus({ preventScroll: true }), 0);
    }
  }

  ngOnDestroy(): void {
    this.slugSub?.unsubscribe();
    this.slugChecks$.complete();
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
    if (this.recheckTimer) clearTimeout(this.recheckTimer);
  }

  /**
   * The live check did not answer. A fast typist trips the probe's rate limit
   * (10, then one per second), and silently dropping back to the idle hint
   * made the check-mark vanish with no explanation. Say so, and for a
   * throttle re-run the check by itself once the server's wait is over.
   */
  private markUnchecked(slug: string, status: number): void {
    this.slugState = 'unknown';
    if (status === 429) {
      this.slugMessage = 'הבדיקה הושהתה לרגע — נבדוק שוב בעוד כמה שניות.';
      if (this.recheckTimer) clearTimeout(this.recheckTimer);
      this.recheckTimer = setTimeout(() => {
        if (this.slug === slug && this.slugState === 'unknown') this.queueSlugCheck();
      }, 2500);
      return;
    }
    this.slugMessage = 'לא הצלחנו לבדוק עכשיו אם הכתובת פנויה — היא תיבדק שוב בפתיחת הערוץ.';
  }

  /** Counters only appear near the limit, so the common case stays quiet. */
  get showNameCounter(): boolean {
    return this.name.length >= this.nameMax - 20;
  }

  get showDescriptionCounter(): boolean {
    return this.description.length >= this.descriptionMax - 200;
  }

  onNameChange(): void {
    if (this.slugTouched) return;
    const suggestion = slugifyChannelName(this.name);
    // Hebrew is transliterated, so this is only empty for a name with nothing
    // slugifiable in it at all — leave whatever the user has rather than wiping
    // a slug they may already be happy with.
    if (!suggestion) return;
    this.slug = suggestion;
    this.queueSlugCheck();
  }

  onSlugChange(): void {
    // Normalise as they type so the field can never hold an illegal character.
    const clean = sanitizeSlugInput(this.slug);
    // An emptied field hands the address back to the name — which is what the
    // "נקבעת לפי השם" chip then says. Judged on the clean value: a field that
    // only ever held illegal characters (Hebrew, punctuation) is empty too,
    // or the chip would promise the name decides while nothing did.
    this.slugTouched = clean.length > 0;
    // [(ngModel)] writes the model back into the <input> only when the bound
    // value changed. When all that was typed is an illegal character the model
    // lands on its previous value, so the input kept showing "abc!" while the
    // model was "abc" — the view is written directly.
    const el = this.slugInput?.nativeElement;
    if (el && el.value !== clean) el.value = clean;
    this.slug = clean;
    this.queueSlugCheck();
  }

  /** Reveals the address field and moves the cursor into it. */
  editSlug(): void {
    this.slugEditing = true;
    setTimeout(() => this.slugInput?.nativeElement.focus(), 0);
  }

  /** One tap on the free variant we found: adopt it and re-verify. */
  useSuggestedSlug(): void {
    if (!this.suggestedSlug) return;
    this.slug = this.suggestedSlug;
    this.slugTouched = true;
    this.suggestedSlug = '';
    this.queueSlugCheck();
  }

  get slugValid(): boolean {
    return SLUG_PATTERN.test(this.slug);
  }

  /** True when the address is syntactically wrong (as opposed to taken). */
  get slugInvalid(): boolean {
    return !!this.slug && !this.slugValid;
  }

  /** The address message is a notice (not a fault) while the check is pending or unavailable. */
  get slugMessageMuted(): boolean {
    return this.slugState === 'unknown';
  }

  /**
   * The button is only locked while a request is in flight or the address is
   * known to be unusable. An empty form stays clickable: the click explains
   * what is missing, which a greyed-out button never does.
   */
  get submitDisabled(): boolean {
    return this.submitting || this.slugState === 'unavailable';
  }

  async submit(): Promise<void> {
    if (this.submitting) return;
    this.formErrorKind = '';
    const name = this.name.trim();
    if (!name) {
      this.formError = 'כדי לפתוח ערוץ צריך לתת לו שם.';
      this.nameInput?.nativeElement.focus();
      return;
    }
    // The address field was emptied and the name not touched since: derive
    // the address now, as the chip promised.
    if (!this.slug && !this.slugTouched) {
      this.slug = slugifyChannelName(name);
      this.queueSlugCheck();
    }
    // Enter right after typing lands here while the live check is still out;
    // wait for its verdict so a taken address is reported as taken, not as
    // "someone beat you to it" after a 409.
    if (this.slugState === 'checking') {
      this.submitting = true;
      try {
        // Typing meanwhile re-queues the check; wait for the last one.
        while (this.slugState === 'checking') await this.pendingVerdict();
      } finally {
        this.submitting = false;
      }
    }
    if (!this.slugValid) {
      this.formError = this.slug
        ? `כתובת הערוץ לא תקינה — ${this.slugMin} עד ${this.slugMax} תווים: אותיות אנגליות קטנות, ספרות ומקפים.`
        : 'לא הצלחנו להרכיב כתובת מהשם הזה — בחרו כתובת באנגלית.';
      this.editSlug();
      return;
    }
    if (this.slugState === 'unavailable') {
      this.formError = 'הכתובת הזו תפוסה — בחרו כתובת אחרת.';
      this.editSlug();
      return;
    }

    this.formError = '';
    this.submitting = true;
    try {
      const channel = await this.channelService.createChannel(
        this.slug, name, this.description.trim(),
      );
      this.createdSlug = channel.slug;
      this.createdName = channel.name || name;
      // The "my channels" list is cached per user; it has to pick the new
      // channel up the next time it is shown.
      this.myChannels.invalidate();
    } catch (err: any) {
      this.handleError(err);
    } finally {
      this.submitting = false;
    }
  }

  async copyUrl(): Promise<void> {
    // The service shows the "copy blocked" notice itself; the URL stays
    // visible and selectable underneath for exactly that case.
    this.copied = await this.share.copy(this.channelUrl);
    if (this.copyResetTimer) clearTimeout(this.copyResetTimer);
    this.copyResetTimer = setTimeout(() => (this.copied = false), 2500);
  }

  async shareSheet(): Promise<void> {
    const outcome = await this.share.share({
      title: this.createdName,
      text: this.inviteText,
      url: this.channelUrl,
    });
    // A browser that advertised the share sheet and then refused still gets
    // the link onto the clipboard.
    if (outcome === 'unsupported') await this.copyUrl();
  }

  enterChannel(): void {
    if (!this.createdSlug) return;
    this.entering = true;
    this.created.emit(this.createdSlug);
  }

  /** The session expired mid-form: go through login and come back here. */
  relogin(): void {
    try {
      localStorage.setItem('returnUrl', '/channel');
    } catch {
      // Storage unavailable — the login page falls back to /channel anyway.
    }
    this.router.navigate(['/login']);
  }

  private handleError(err: any): void {
    // Every one of these endpoints answers failures as plain text (http.Error),
    // which Angular hands back as a string in err.error. The text is matched
    // to pick the right explanation and never shown as is.
    const text = typeof err?.error === 'string' && err.error
      ? err.error
      : (err?.error?.message || '');

    switch (err?.status) {
      case 409:
        // The server answers 409 for two unrelated things: 'slug already
        // taken' and the per-account creation lock ('another channel is
        // already being created for this account', e.g. a second tab or a
        // retry within its window). The lock is not the slug's fault, so it
        // must not tell the user to pick another one.
        if (text.includes('already being created')) {
          this.formError = 'ערוץ אחר שלכם עדיין נפתח ברגע זה (אולי בחלון אחר). חכו רגע ונסו שוב.';
          return;
        }
        // Inline on the address — that is what they have to change — plus a
        // free variant they can take in one tap.
        this.slugState = 'unavailable';
        this.slugMessage = 'מישהו הקדים אתכם — הכתובת הזו תפוסה.';
        this.formError = '';
        this.slugEditing = true;
        this.suggestFreeSlug(this.slug, 5);
        return;
      case 403:
        this.formErrorKind = 'limit';
        this.formError = `אפשר לפתוח עד ${this.maxChannels} ערוצים לחשבון, וכבר יש לכם ${this.maxChannels}. כדי לפתוח ערוץ נוסף פנו להנהלת המערכת ("פנייה לתמיכה" בתפריט החשבון).`;
        return;
      case 429: {
        const minutes = retryAfterMinutes(err) ?? 20;
        this.formError = `פתחתם כמה ערוצים ברצף — אפשר לנסות שוב בעוד כ-${minutes} דקות.`;
        return;
      }
      case 400:
        if (text.includes('too long')) {
          this.formError = `השם או התיאור ארוכים מדי (עד ${this.nameMax} תווים לשם ועד ${this.descriptionMax} לתיאור).`;
          return;
        }
        if (text.includes('name is required')) {
          this.formError = 'כדי לפתוח ערוץ צריך לתת לו שם.';
          return;
        }
        if (text.includes('reserved')) {
          this.slugState = 'unavailable';
          this.slugMessage = this.reasonText('reserved');
          this.slugEditing = true;
          this.formError = '';
          return;
        }
        if (text.includes('slug')) {
          this.slugState = 'unavailable';
          this.slugMessage = this.reasonText('invalid');
          this.slugEditing = true;
          this.formError = '';
          return;
        }
        this.formError = 'הפרטים שהוזנו לא התקבלו. בדקו את השם והכתובת ונסו שוב.';
        return;
      case 401:
        this.formErrorKind = 'auth';
        this.formError = 'פג תוקף ההתחברות. התחברו מחדש כדי לפתוח את הערוץ.';
        return;
      case 0:
        this.formError = 'אין חיבור לשרת. בדקו את החיבור לאינטרנט ונסו שוב.';
        return;
      default:
        this.formError = 'משהו השתבש בפתיחת הערוץ. נסו שוב בעוד רגע.';
    }
  }

  // Resolved whenever the live check leaves 'checking' (see submit()).
  private verdictWaiters: (() => void)[] = [];

  private pendingVerdict(): Promise<void> {
    if (this.slugState !== 'checking') return Promise.resolve();
    return new Promise<void>(resolve => this.verdictWaiters.push(resolve));
  }

  private settleVerdict(): void {
    const waiters = this.verdictWaiters;
    this.verdictWaiters = [];
    waiters.forEach(w => w());
  }

  private queueSlugCheck(): void {
    this.formError = '';
    this.suggestedSlug = '';
    // Whatever was being checked is superseded; a submit waiting on it looks
    // at the new state (and waits again if that is 'checking').
    this.settleVerdict();
    // Bumping the sequence orphans a running suggestFreeSlug(): its finally
    // block no longer owns the flag, so it is cleared here or the
    // "looking for a free address" line would stay on for good.
    this.suggestSeq++;
    this.suggesting = false;
    if (this.recheckTimer) clearTimeout(this.recheckTimer);
    if (!this.slug) {
      this.slugState = 'empty';
      this.slugMessage = '';
      return;
    }
    if (!this.slugValid) {
      this.slugState = 'unavailable';
      this.slugMessage = this.slug.length < this.slugMin
        ? `הכתובת קצרה מדי — לפחות ${this.slugMin} תווים.`
        : this.reasonText('invalid');
      return;
    }
    this.slugState = 'checking';
    this.slugMessage = '';
    this.slugChecks$.next(this.slug);
  }

  /** Looks for `base-2`, `base-3`… and offers the first free one. */
  private async suggestFreeSlug(base: string, maxTries: number): Promise<void> {
    const seq = ++this.suggestSeq;
    this.suggesting = true;
    try {
      const free = await this.channelService.findFreeSlugVariant(base, maxTries);
      // The user may have typed on meanwhile; only answer the question asked.
      if (seq !== this.suggestSeq || base !== this.slug) return;
      this.suggestedSlug = free ?? '';
    } finally {
      if (seq === this.suggestSeq) this.suggesting = false;
    }
  }

  private reasonText(reason: SlugAvailability['reason']): string {
    switch (reason) {
      case 'taken': return 'הכתובת הזו כבר תפוסה — בחרו אחרת.';
      case 'reserved': return 'הכתובת הזו שמורה למערכת — בחרו אחרת.';
      case 'invalid': return 'רק אותיות אנגליות קטנות, ספרות ומקפים (לא בתחילת הכתובת ולא בסופה).';
      default: return 'הכתובת הזו לא זמינה — בחרו אחרת.';
    }
  }
}
