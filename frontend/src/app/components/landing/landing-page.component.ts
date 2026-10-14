import { Component, HostListener, OnInit } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { ActivatedRoute, RouterLink, Router } from '@angular/router';
import {
  NbCardModule, NbButtonModule, NbIconModule, NbLayoutModule,
} from '@nebular/theme';
import { AuthService } from '../../services/auth.service';
import { SupportBoxComponent } from '../support/support-box.component';

interface LandingStep {
  icon: string;
  title: string;
  text: string;
}

interface LandingFaq {
  q: string;
  a: string;
}

interface LandingFeature {
  icon: string;
  title: string;
  text: string;
}

@Component({
  selector: 'app-landing-page',
  standalone: true,
  imports: [
    RouterLink,
    NbCardModule,
    NbButtonModule,
    NbIconModule,
    NbLayoutModule,
    SupportBoxComponent,
  ],
  // Two sheets so each stays under the component-style budget: the hero (with
  // its mock channel preview) and everything below it.
  styleUrls: ['./landing-page.component.scss', './landing-hero.scss'],
  template: `
    <!-- nb-layout must wrap the page — it is what applies the nb-theme-* class
         that every Nebular CSS variable hangs off. Without it the whole page
         renders unstyled.
         The page is rendered right away, not after /api/user-info answers: an
         anonymous visitor (the page's audience) used to stare at a blank
         column for a full round trip; a signed-in visitor now sees the hero
         for a moment before being sent on, which is the cheaper of the two. -->
    <nb-layout>
      <nb-layout-column>
    <nav class="topbar" aria-label="ניווט ראשי">
      <span class="topbar__brand">
        <span class="brand-mark" aria-hidden="true"><nb-icon aria-hidden="true" icon="radio-outline"></nb-icon></span>
        הערוץ
      </span>
      <a nbButton ghost status="primary" size="small" [routerLink]="['/login']">
        <nb-icon aria-hidden="true" icon="log-in-outline"></nb-icon>
        התחברות
      </a>
    </nav>

    <!-- Hero -->
    <header class="hero">
      <div class="hero__inner">
        <div class="hero__copy">
          <span class="hero__badge">
            <nb-icon aria-hidden="true" icon="flash-outline"></nb-icon>
            בחינם · בלי אפליקציה · בלי המתנה לאישור
          </span>
          <h1 class="hero__title">ערוץ עדכונים משלכם, פתוח תוך רגע</h1>
          <p class="hero__lead">
            ערוץ הוא דף אחד שבו אתם מפרסמים הודעות, תמונות, סרטונים וקישורים,
            והם מגיעים לכל מי שיש לו את הקישור. חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל:
            הקוראים לא צריכים להתקין כלום ולא להירשם.
          </p>
          <div class="hero__actions">
            <button nbButton status="primary" size="large" class="hero__cta" type="button" (click)="startCreate()" [disabled]="starting">
              <nb-icon aria-hidden="true" icon="flash-outline"></nb-icon>
              פתיחת ערוץ — חינם
            </button>
            <a nbButton appearance="outline" size="large" class="hero__alt" [routerLink]="['/login']">
              יש לי כבר ערוץ / התחברות
            </a>
          </div>
          <p class="hero__note">
            מתחברים עם חשבון גוגל (בלי סיסמה נוספת) — רק כדי שהערוץ יירשם על שמכם.
          </p>
        </div>

        <!-- A mock of a channel page, so the visitor sees what they get before
             signing in. Decorative: hidden from screen readers. -->
        <div class="preview" aria-hidden="true">
          <div class="preview__head">
            <span class="preview__avatar">ח</span>
            <div class="preview__text">
              <span class="preview__name">חדשות השכונה</span>
              <span class="preview__meta">1,248 משתתפים · <i class="preview__live"></i> עדכונים חיים</span>
            </div>
          </div>
          <div class="preview__feed">
            <div class="preview__msg">
              <span class="preview__msg-text">בוקר טוב! הגינה הקהילתית נפתחת היום ב-16:00. מביאים כלי גינון ומצב רוח טוב 🌱</span>
              <span class="preview__msg-foot"><span class="preview__react">👍 42</span><span class="preview__react">❤️ 18</span><span class="preview__time">09:12</span></span>
            </div>
            <div class="preview__msg">
              <span class="preview__img"><nb-icon aria-hidden="true" icon="image-outline"></nb-icon></span>
              <span class="preview__msg-text">תמונות מהשבוע שעבר — תודה לכל מי שהגיע!</span>
              <span class="preview__msg-foot"><span class="preview__react">🔥 27</span><span class="preview__time">אתמול</span></span>
            </div>
            <div class="preview__msg preview__msg--sched">
              <span class="preview__msg-text"><nb-icon aria-hidden="true" icon="clock-outline"></nb-icon> מתוזמן למחר 08:00 · תזכורת: אסיפת דיירים</span>
            </div>
          </div>
        </div>
      </div>
    </header>

    <!-- How it works -->
    <section class="section" aria-labelledby="how-title">
      <div class="wrap">
        <h2 class="section__title" id="how-title">איך זה עובד</h2>
        <p class="section__lead">שלושה צעדים, דקה אחת — ואתם משדרים.</p>
        <ol class="steps">
          @for (step of steps; track step.title; let i = $index) {
            <li class="step">
              <span class="step__num">{{ i + 1 }}</span>
              <nb-icon aria-hidden="true" [icon]="step.icon" class="step__icon"></nb-icon>
              <h3 class="step__title">{{ step.title }}</h3>
              <p class="step__text">{{ step.text }}</p>
            </li>
          }
        </ol>
      </div>
    </section>

    <!-- What you get -->
    <section class="section section--alt" aria-labelledby="features-title">
      <div class="wrap">
        <h2 class="section__title" id="features-title">מה יש בערוץ</h2>
        <p class="section__lead">הכול כלול, מהרגע הראשון, בלי תוספות.</p>
        <div class="features">
          @for (feature of features; track feature.title) {
            <div class="feature">
              <span class="feature__icon-wrap"><nb-icon aria-hidden="true" [icon]="feature.icon" class="feature__icon"></nb-icon></span>
              <div>
                <h3 class="feature__title">{{ feature.title }}</h3>
                <p class="feature__text">{{ feature.text }}</p>
              </div>
            </div>
          }
        </div>
      </div>
    </section>

    <!-- Who it is for -->
    <section class="section section--tight" aria-labelledby="who-title">
      <div class="wrap wrap--narrow">
        <h2 class="section__title section__title--small" id="who-title">למי זה מתאים</h2>
        <ul class="audience">
          @for (item of audience; track item) {
            <li class="audience__item">{{ item }}</li>
          }
        </ul>
      </div>
    </section>

    <!-- The questions come from the FAQPage data the server put in the page
         (backend/seo_content.go), so search results and this list say the
         same thing. Absent (e.g. under ng serve), the section is skipped. -->
    @if (faq.length) {
      <section class="section section--alt" aria-labelledby="faq-title">
        <div class="wrap wrap--narrow">
          <h2 class="section__title" id="faq-title">שאלות נפוצות</h2>
          <div class="faq">
            @for (item of faq; track item.q) {
              <details class="faq__item">
                <summary class="faq__q">{{ item.q }}</summary>
                <p class="faq__a">{{ item.a }}</p>
              </details>
            }
          </div>
        </div>
      </section>
    }

    <!-- Closing CTA — the one and only way in -->
    <section class="section cta" aria-labelledby="cta-title">
      <div class="wrap wrap--narrow cta__inner">
        <h2 class="section__title" id="cta-title">מוכנים להתחיל?</h2>
        <p class="cta__text">
          התחברות עם חשבון גוגל, שם לערוץ — וזהו. תוך רגע יש לכם קישור לשתף.
        </p>
        <button nbButton status="primary" size="large" class="cta__button" type="button" (click)="startCreate()" [disabled]="starting">
          <nb-icon aria-hidden="true" icon="flash-outline"></nb-icon>
          פתיחת ערוץ — חינם
        </button>
      </div>
    </section>

    <!-- Reaching the operator must not require an account: someone who cannot
         sign in, or is deciding whether to, is exactly who needs to ask. -->
    <section class="section section--alt support" id="contact" aria-labelledby="contact-title">
      <div class="wrap wrap--narrow">
        <h2 class="section__title" id="contact-title">יש שאלה?</h2>
        <!-- From an emailed link the box waits for the session check, so it
             loads the right threads (session or this browser's) the first time. -->
        @if (!supportLink || authChecked) {
          <app-support-box
            [signedIn]="signedIn"
            [channelSlug]="supportChannel"
            [openTicketId]="supportTicket"
            title="פנייה אלינו"
            subtitle="שאלה לפני פתיחת ערוץ, תקלה או בקשה — כתבו לנו ונחזור אליכם.">
          </app-support-box>
        }
      </div>
    </section>

    <footer class="footer">
      <span class="footer__brand">הערוץ</span>
      <span class="footer__sep" aria-hidden="true">·</span>
      <span>&copy; {{ year }}</span>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" [routerLink]="['/login']">התחברות</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <!-- Server-rendered pages, outside the app: plain links, full load. -->
      <a class="footer__link" href="/open-channel">מדריך לפתיחת ערוץ</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="/community-channel">ערוץ הודעות לקהילה</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="/whatsapp-alternative">חלופה לערוץ וואטסאפ</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="/netfree-channel">ערוץ בנטפרי</a>
      <span class="footer__sep" aria-hidden="true">·</span>
      <a class="footer__link" href="#contact" (click)="scrollToContact($event)">יצירת קשר</a>
    </footer>
      </nb-layout-column>
    </nb-layout>
  `,
})
export class LandingPageComponent implements OnInit {
  /** Only decides which fields the contact form asks for; the server takes the
   *  sender's identity from the session regardless of what is posted. */
  signedIn = false;
  readonly year = new Date().getFullYear();

  /**
   * Set by `?support=1`, the link the system's emails carry (the channel
   * welcome mail and a support answer): open the contact box, for a signed-in
   * visitor too, who would otherwise be sent straight on to their channels.
   * `channel` tags a new ticket with that channel; `ticket` opens that thread.
   */
  supportLink = false;
  supportChannel = '';
  supportTicket = '';
  authChecked = false;

  readonly steps: LandingStep[] = [
    { icon: 'log-in-outline', title: 'מתחברים עם גוגל', text: 'לחיצה אחת על חשבון הגוגל שלכם. בלי סיסמה חדשה ובלי טפסים.' },
    { icon: 'edit-2-outline', title: 'בוחרים שם', text: 'נותנים לערוץ שם; הכתובת שלו נקבעת מהשם ונבדקת שהיא פנויה.' },
    { icon: 'share-outline', title: 'משתפים את הקישור', text: 'שולחים את הקישור בוואטסאפ או בכל דרך — וכל מי שפותח אותו רואה את הערוץ.' },
  ];

  readonly features: LandingFeature[] = [
    { icon: 'flash-outline', title: 'עדכונים חיים', text: 'הודעה שפרסמתם מופיעה אצל הקוראים באותו רגע, בלי לרענן.' },
    { icon: 'image-outline', title: 'תמונות וסרטונים', text: 'מעלים תמונות, סרטונים וקבצים ישירות לערוץ. יוטיוב מתנגן בפנים.' },
    { icon: 'smiling-face-outline', title: 'תגובות אימוג׳י', text: 'הקוראים מגיבים בלחיצה — ואתם רואים מה תפס.' },
    { icon: 'clock-outline', title: 'הודעות מתוזמנות', text: 'כותבים עכשיו, קובעים מתי יתפרסם — והערוץ מפרסם לבד.' },
    { icon: 'people-outline', title: 'צוות כותבים ומנהלים', text: 'מצרפים עוד אנשים שיכתבו וינהלו, כל אחד עם ההרשאה שלו.' },
    { icon: 'bell-outline', title: 'התראות לנייד', text: 'קורא שמבקש לקבל התראות יקבל הודעה לנייד על כל פרסום חדש.' },
  ];

  readonly audience = [
    'קהילות ושכונות', 'בתי ספר וגנים', 'עסקים והלקוחות שלהם',
    'ארגונים ומתנדבים', 'יוצרים והקהל שלהם', 'משפחה וחברים',
  ];

  readonly faq: LandingFaq[] = readServerFaq();

  /** True from the CTA click until the browser leaves for Google. */
  starting = false;

  constructor(
    private authService: AuthService,
    private router: Router,
    private route: ActivatedRoute,
    private titleService: Title,
  ) {}

  async ngOnInit() {
    // The same title the server sends for "/" (homeTitle in
    // backend/seo_content.go), so arriving here from inside the app does not
    // change the tab.
    const siteName = document.querySelector('meta[name="application-name"]')?.getAttribute('content') || 'הערוץ';
    this.titleService.setTitle(`פתיחת ערוץ עדכונים בחינם, בלי אפליקציה · ${siteName}`);

    const q = this.route.snapshot.queryParamMap;
    this.supportLink = q.get('support') === '1';
    if (this.supportLink) {
      // Both are only labels/lookups (the server authorises every thread), but
      // they come from a URL, so anything that is not a plain id is dropped.
      const channel = q.get('channel') ?? '';
      const ticket = q.get('ticket') ?? '';
      this.supportChannel = /^[a-z0-9-]{1,64}$/.test(channel) ? channel : '';
      this.supportTicket = /^[A-Za-z0-9_-]{1,64}$/.test(ticket) ? ticket : '';
      try {
        this.signedIn = !!(await this.authService.loadUserInfo());
      } catch {
        this.signedIn = false;
      }
      this.authChecked = true;
      // After the box has rendered under the hero.
      setTimeout(() => document.getElementById('contact')?.scrollIntoView({ block: 'start' }));
      return;
    }

    try {
      const user = await this.authService.loadUserInfo();
      if (user) {
        if (user.globalRole === 'super_admin') {
          this.router.navigate(['/super-admin']);
        } else {
          this.router.navigate(['/channel']);
        }
        return;
      }
    } catch {
      // Not logged in — the landing page is already on screen.
    }
  }

  /**
   * The footer's "contact" link. A plain fragment link depends on the window
   * being the scroll container, which nb-layout does not guarantee, so the
   * section is scrolled into view explicitly.
   */
  scrollToContact(event: Event): void {
    const target = document.getElementById('contact');
    if (!target) return;
    event.preventDefault();
    target.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  /**
   * Instant creation needs a session (the backend takes the owner from it), so
   * the primary CTA sends visitors through login and back to /channel, where a
   * user with no channel lands on the creation form.
   * `returnUrl` lives in localStorage — that is what LoginComponent reads.
   */
  async startCreate(): Promise<void> {
    if (this.starting) return;
    try {
      localStorage.setItem('returnUrl', '/channel');
    } catch {
      // Storage unavailable — the login page falls back to /channel anyway.
    }
    // Straight to Google: a second screen with the same button before the
    // consent page is a click nobody needs. The login page remains the OAuth
    // callback and the place to land (with its explanation and retry) when
    // the hand-off itself fails.
    this.starting = true;
    try {
      await this.authService.loginWithGoogle();
    } catch {
      this.starting = false;
      this.router.navigate(['/login'], { queryParams: { returnUrl: '/channel' } });
    }
  }

  // Back from Google's account picker restores this page from the
  // back-forward cache with `starting` still true, i.e. both CTAs greyed out.
  @HostListener('window:pageshow', ['$event'])
  onPageShow(event: PageTransitionEvent) {
    if (event.persisted) this.starting = false;
  }
}

/**
 * The FAQ from the page's FAQPage structured data. Read once, when the
 * module loads: that is the document the server rendered for the first URL,
 * which is "/" for nearly everyone who sees this page.
 */
function readServerFaq(): LandingFaq[] {
  try {
    for (const el of Array.from(document.querySelectorAll('script[type="application/ld+json"]'))) {
      const data = JSON.parse(el.textContent || '{}');
      const nodes: any[] = Array.isArray(data?.['@graph']) ? data['@graph'] : [data];
      const page = nodes.find(n => n?.['@type'] === 'FAQPage');
      if (!page || !Array.isArray(page.mainEntity)) continue;
      return page.mainEntity
        .map((q: any) => ({ q: String(q?.name ?? ''), a: String(q?.acceptedAnswer?.text ?? '') }))
        .filter((f: LandingFaq) => f.q && f.a);
    }
  } catch {
    // Malformed or missing data: the page simply has no FAQ section.
  }
  return [];
}
