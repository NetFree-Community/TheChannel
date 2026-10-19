import { Hooks, Token, Tokens, TokensList } from "marked";
import { MarkdownModuleConfig, MARKED_EXTENSIONS, MARKED_OPTIONS, MarkedRenderer } from "ngx-markdown";
import DOMPurify, { UponSanitizeAttributeHookEvent } from "dompurify";

/**
 * Embed token: `[image-embedded#](url)`, optionally carrying the media's pixel
 * size as `[image-embedded#800x600](url)`.
 *
 * The size group is optional so every message written before the backend
 * started recording dimensions still tokenizes exactly as it did — it simply
 * renders without an aspect ratio, as it always has. The size lives in the
 * token rather than in the URL so the `src` stays byte-identical to the path
 * the server handed out.
 */
const matchCustomEmbedRegEx = /^\[(video|audio|image|quote)-embedded#(\d+x\d+)?]\((.*?)\)/;

/**
 * The quote token is matched on its own, before the generic pattern, and runs
 * to the last ')' on its line. The generic pattern is non-greedy, so a quoted
 * text that itself contained ')' — "(ראו למטה)" — ended the token at that
 * first ')' and the remainder of the quote leaked into the message as plain
 * text. The composer always terminates a quote token with a newline
 * (message.component.ts quoteMessage), so nothing else shares its line.
 */
// Greedy to the last ')' so a quoted text with parentheses is not cut, but
// only when the token ends the line (the composer always terminates it with a
// newline); a legacy message with reply text on the same line falls back to
// the non-greedy generic pattern below, or that text would be swallowed.
const matchQuoteEmbedRegEx = /^\[quote-embedded#]\(([^\n]*)\)[ \t]*(?=\n|$)/;

/**
 * The embed token's opening bracket, without the payload — for callers that
 * only need to know a message *is* an embed and of which kind (capture group 1).
 *
 * Exported so it cannot drift from the tokenizer above. When the optional size
 * was added, a private copy of this pattern in the message component still
 * required `#]` immediately, so an image posted with its dimensions stopped
 * being recognised and quoting one embedded raw markdown into the reply.
 */
export const EMBED_PREFIX_REGEX = /^\[(video|audio|image|quote)-embedded#(?:\d+x\d+)?]/;

/**
 * Only the URL forms that carry a video id: watch?v=, embed/, v/, shorts/ and
 * youtu.be/, each followed by the 11-character id. The previous pattern
 * (regexr.com/3dj5t) made the path segment optional, so youtube.com/shorts/ID,
 * /playlist?list=… and /channel/UC… all matched with "shorts", "playlist" or
 * "channel" captured as the id — a broken thumbnail and a player that could
 * not play. Its trailing `$` (no `m` flag) also meant a URL followed by a line
 * break and more text was never embedded at all; the lookahead accepts any
 * whitespace, so the `breaks: true` line break no longer defeats it. The
 * scheme stays optional, as before, so a bare www.youtube.com/… still embeds.
 */
const matchYoutubeRegEx = /^(?:(?:https?:)?\/\/)?(?:(?:www|m)\.)?(?:youtube\.com\/(?:watch\?(?:[^\s#]*&)?v=|embed\/|v\/|shorts\/)|youtu\.be\/)(?<id>[\w-]{11})(?:[^\s]*)?(?=\s|$)/;

/**
 * Every image the feed renders goes out with native lazy loading: messages
 * arrive 20 at a time through nbInfiniteList, but without this each one's
 * media is fetched the moment the message enters the DOM, so a media-heavy
 * channel opens by downloading every picture in the batch at once.
 *
 * `decoding="async"` keeps the decode off the main thread, so a picture
 * arriving mid-scroll cannot stall the list.
 *
 * Lazy loading alone makes the feed grow as pictures arrive, which shoves the
 * reader's position around. `sizeAttrs` below prevents that wherever the size
 * is known.
 */
const LAZY_IMG_ATTRS = ' loading="lazy" decoding="async"';

/**
 * Renders a `WxH` size token as width/height attributes. Bootstrap's
 * `.img-fluid` is `max-width: 100%; height: auto`, so the browser uses the
 * pair purely as an aspect ratio: the picture still fits the 300px box, but
 * the box reserves the right height before the bytes land and nothing below it
 * jumps when they do.
 *
 * An absent or malformed size yields no attributes at all — every message
 * predating dimension recording, and any upload whose header could not be
 * parsed, keeps exactly the old behaviour rather than getting a guessed ratio
 * that would letterbox or distort it.
 */
function sizeAttrs(size: string | undefined): string {
  const m = /^(\d+)x(\d+)$/.exec(size ?? '');
  if (!m) return '';
  const w = Number(m[1]), h = Number(m[2]);
  if (!w || !h) return '';
  return ` width="${w}" height="${h}"`;
}


/**
 * Everything below builds HTML by string concatenation and the feed renders it
 * with `[disableSanitizer]="true"`, so Angular does not get a second look at
 * it. Every value interpolated into markup therefore has to be neutralised
 * here — this is the only line of defence.
 *
 * Without it, message text was executable: `[image-embedded#](x" onerror="...)`
 * closed the src attribute and added an event handler, and the quote branch
 * interpolated free text straight into the document. Any writer could run
 * script in every viewer's browser, including a channel owner's or a super
 * admin's.
 */
function escapeHtml(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Neutralises a URL destined for a src/href attribute.
 *
 * A scheme is only honoured when it is http or https, so `javascript:`,
 * `data:` and `vbscript:` collapse to an empty attribute instead of executing.
 * Relative URLs — which is what the upload path produces
 * (/api/channel/<slug>/files/<id>) — carry no scheme and pass through. Control
 * characters are dropped first, since `java\tscript:` is the same URL to a
 * browser but not to a naive prefix test.
 */
function safeUrl(value: unknown): string {
  return cleanUrl(value, /^https?$/i);
}

/**
 * The same, for an anchor's href, where mailto: and tel: are honoured as well.
 * GFM autolinks turn office@example.com into a mailto: link, and safeUrl's
 * http(s)-only rule blanked that href: the address rendered as a link that
 * went nowhere. src attributes keep the strict rule — the browser would only
 * ever be asked to *fetch* a src, and there is nothing to fetch from mailto:.
 */
function safeHref(value: unknown): string {
  return cleanUrl(value, /^(https?|mailto|tel)$/i);
}

function cleanUrl(value: unknown, allowedSchemes: RegExp): string {
  const url = String(value ?? '').replace(/[\u0000-\u001F\u007F]/g, '').trim();
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(url);
  if (scheme && !allowedSchemes.test(scheme[1])) return '';
  return escapeHtml(url);
}

const customEmbedExtension = {
  extensions: [{
    name: 'custom_embed',
    level: 'inline',
    start: (src: string) => src.match(matchQuoteEmbedRegEx)?.index ?? src.match(matchCustomEmbedRegEx)?.index ?? src.match(matchYoutubeRegEx)?.index,
    tokenizer: (src: string, tokens: Token[] | TokensList) => {

      const quote = src.match(matchQuoteEmbedRegEx);
      // The strict pattern runs to the LAST ')' of the line, so a legacy
      // same-line reply that ends with a markdown link —
      // `[quote-embedded#](12@abc) see [link](https://…)` — was swallowed
      // whole: the link gone, its URL shown as quote text. Decide by what the
      // strict match takes BEYOND the non-greedy one: a link there belongs to
      // the reply, so the non-greedy branch below wins; a link inside the
      // quoted text itself (old quotes, before the composer stripped
      // parentheses) stays with the quote.
      const loose = src.match(matchCustomEmbedRegEx);
      if (quote && !(loose && quote[0].slice(loose[0].length).includes(']('))) {
        const s = quote[1].split(/@(.*)/);
        return {
          type: 'custom_embed',
          raw: quote[0],
          meta: { type: 'quote', id: s[0], url: s[1] },
        };
      }

      let match = src.match(matchCustomEmbedRegEx);
      if (match) {
        if (match[1] === 'quote') {
          // A legacy quote with reply text on the same line lands here (the
          // strict pattern above needs the token to end its line). Its payload
          // is still `id@text`; handing it over unsplit rendered an empty
          // quote-id and the raw "12@abc" as the quote's text.
          const s = match[3].split(/@(.*)/);
          return {
            type: 'custom_embed',
            raw: match[0],
            meta: { type: 'quote', id: s[0], url: s[1] },
          };
        }
        return {
          type: 'custom_embed',
          raw: match[0],
          meta: { type: match[1], url: match[3], size: match[2] },
        };
      }

      match = src.match(matchYoutubeRegEx);
      if (match && match.groups?.['id']) {
        return {
          type: 'custom_embed',
          raw: match[0],
          meta: { type: 'youtube', id: match.groups['id'] },
        };
      }

      return undefined;
    },
    renderer: (token: Tokens.Generic) => {
      const { type, url, id, size } = token['meta'];
      // No inline style anywhere below: the sanitizer strips every `style`
      // attribute (see sanitizeFeedHtml), so the embeds' own sizing lives in
      // the embed-* classes, defined in message.component.scss and listed in
      // FEED_CLASSES.
      switch (type) {
        case 'video':
          // metadata, not auto: enough for the poster frame and duration
          // without pulling the whole file for a message nobody scrolled to.
          // The size token is not emitted for video, so this is inert today;
          // it costs nothing and lets a recorded size reserve the player box
          // if the upload path ever measures video too.
          return `<div class="embed-box"><video controls preload="metadata" class="embed-video"${sizeAttrs(size)}><source src="${safeUrl(url)}" type="video/mp4"></video></div>`;
        case 'audio':
          return `<div><audio src="${safeUrl(url)}" controls preload="metadata"></audio></div>`;
        case 'image':
          // The real size when the upload path recorded it, so the box
          // reserves the right height; the old flat width="300" otherwise.
          return `<div class="embed-box"><img src="${safeUrl(url)}"${LAZY_IMG_ATTRS} class="img-fluid"${sizeAttrs(size) || ' width="300"'}></div>`;
        case 'youtube':
          // The play badge is drawn in CSS on .embed-youtube-icon (message
          // component); the icon font that used to render it is gone.
          return `<div class="embed-box embed-youtube"><img youtubeid="${escapeHtml(id)}" src="https://ytimg.googleusercontent.com/vi/${encodeURIComponent(String(id))}/hqdefault.jpg"${LAZY_IMG_ATTRS} class="img-fluid" width="300" height="225"><i
          class="embed-youtube-icon" youtubeid="${escapeHtml(id)}" role="button" aria-label="הפעלת הסרטון"></i></div>`;
        case 'quote':
          return `<blockquote class="quote" quote-id="${escapeHtml(id)}"><p>${escapeHtml(url)}</p></blockquote>`;
        default:
          return '';
      }
    }
  }]
}

const renderer = new MarkedRenderer();

//https://github.com/jfcere/ngx-markdown/issues/79#issuecomment-2484682034
renderer.link = ({ href, text }) => {
  // Overriding marked's link renderer also discards its escaping and its
  // cleanUrl scheme check, so both have to be reapplied here.
  const url = safeHref(href);
  // mailto:/tel: hand off to the mail or phone app; opened in a new tab they
  // leave an empty tab behind. Everything else — including uploaded files at
  // /api/... — still opens in a new tab so the reader keeps their place.
  const target = /^(mailto|tel):/i.test(url) ? '' : ' target="_blank" rel="noopener noreferrer"';
  return `<a${target} href="${url}">${escapeHtml(text)}</a>`;
}

// Plain markdown images (![alt](url)) bypass the custom_embed extension, so
// they need the same lazy treatment — this is the second and last place the
// feed can emit an <img>.
renderer.image = ({ href, title, text }) => {
  const titleAttr = title ? ` title="${escapeHtml(title)}"` : '';
  return `<img src="${safeUrl(href)}" alt="${escapeHtml(text)}"${titleAttr}${LAZY_IMG_ATTRS} class="img-fluid">`;
}
//renderer.paragraph = ({ tokens }) => Parser.parseInline(tokens);

/**
 * Final sanitization of the rendered feed HTML.
 *
 * The feed binds this output with `[disableSanitizer]="true"`, so Angular never
 * gets a second look — and marked passes RAW HTML in the message body straight
 * through its default renderer. Per-value escaping in the custom renderers above
 * only covers what THEY build; a message body of `<img src=x onerror=alert(1)>`
 * or `<a href="javascript:...">` reached the DOM verbatim and executed. This is
 * the actual trust boundary, and it has to allow the small set of markup the
 * feed legitimately produces (the media/quote/link embeds, and the `<u>` the
 * formatting toolbar inserts) while removing everything dangerous.
 *
 * DOMPurify strips event handlers and javascript:/data: URLs by default; the
 * config below additionally forbids elements the feed never needs and that are
 * common injection vectors, and re-permits the two non-standard attributes the
 * embeds rely on (youtubeid, quote-id) plus target/rel on links.
 */
/**
 * DOMPurify allows `data:` URIs on media tags (img/video/audio/source)
 * independent of ALLOWED_URI_REGEXP, as a legitimate way to inline images. The
 * feed never inlines media that way — every image is a relative /api/... URL or
 * an https thumbnail — so this hook strips any src that is not http(s) or
 * relative, and any href that is not that, mailto: or tel: (the schemes
 * safeHref honours). A `data:text/html` src on an img is inert on its own (an
 * img never runs its src as a document), but removing it leaves no room for
 * doubt and matches the backend's own scheme allow-list.
 */
const URL_ATTR_SCHEMES: ReadonlyArray<[string, RegExp]> = [
  ['src', /^https?$/i],
  ['href', /^(https?|mailto|tel)$/i],
];

DOMPurify.addHook('afterSanitizeAttributes', (node: Element) => {
  for (const [attr, allowed] of URL_ATTR_SCHEMES) {
    const value = node.getAttribute?.(attr);
    if (value === null || value === undefined) continue;
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(value.trim());
    if (scheme && !allowed.test(scheme[1])) {
      node.removeAttribute(attr);
    }
  }
});

/**
 * The only classes the feed's own markup uses: what the renderers above emit,
 * plus the `language-*` marker marked puts on fenced code for Prism.
 *
 * bootstrap.min.css is loaded globally, so a `class` attribute in raw HTML
 * handed a writer the whole layout toolkit: `<div class="position-fixed top-0
 * start-0 vw-100 vh-100 bg-white z-3">` covered the page for every viewer, and
 * a `style` attribute did the same with no framework at all — a phishing
 * overlay in a message. `style` is therefore forbidden outright in
 * sanitizeFeedHtml, and a `class` attribute survives only when every one of
 * its tokens is on this list; otherwise the attribute is dropped and the
 * element itself (the `<u>`, `<b>`, `<br>` older messages carry) stays as it
 * was.
 */
const FEED_CLASSES: ReadonlySet<string> = new Set([
  'img-fluid', 'quote', 'bi', 'bi-youtube',
  'embed-box', 'embed-video', 'embed-youtube', 'embed-youtube-icon',
]);
const FEED_CLASS_PATTERN = /^language-[\w-]+$/;

DOMPurify.addHook('uponSanitizeAttribute', (_node: Element, data: UponSanitizeAttributeHookEvent) => {
  if (data.attrName !== 'class') return;
  const tokens = data.attrValue.split(/\s+/).filter(Boolean);
  if (!tokens.every(t => FEED_CLASSES.has(t) || FEED_CLASS_PATTERN.test(t))) {
    data.keepAttr = false;
  }
});

function sanitizeFeedHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    // Non-standard attributes the embed renderers emit and the message
    // component reads (message.component.ts reads youtubeid); target/rel are on
    // the link renderer's anchors.
    ADD_ATTR: ['youtubeid', 'quote-id', 'target', 'rel'],
    // The feed emits none of these, and each is a classic injection vector.
    // Event handlers and dangerous URL schemes are removed by DOMPurify
    // regardless of this list.
    FORBID_TAGS: ['style', 'iframe', 'form', 'input', 'button', 'object', 'embed', 'svg', 'math', 'link', 'meta', 'base'],
    // Inline style is a page-wide overlay waiting to happen (see FEED_CLASSES);
    // nothing the feed renders needs it.
    FORBID_ATTR: ['style'],
    // Only the schemes the feed actually uses: https/http (youtube, external
    // links), mailto and tel (autolinked addresses and numbers), and relative
    // URLs (uploaded files are /api/channel/...). This also removes the
    // inert-but-pointless data: image case, matching the backend's own safeUrl
    // scheme allow-list.
    ALLOWED_URI_REGEXP: /^(?:https?:|mailto:|tel:|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i,
  }) as unknown as string;
}

export const MarkdownConfig: MarkdownModuleConfig = {
  markedExtensions: [
    {
      provide: MARKED_EXTENSIONS,
      useValue: customEmbedExtension,
      multi: true,
    },
  ],
  markedOptions: {
    provide: MARKED_OPTIONS,
    useValue: {
      renderer: renderer,
      breaks: true,
      // Sanitize after marked has produced the HTML — including any raw HTML in
      // the message body — so it is the last thing before the string is bound.
      //
      // This MUST be a real marked Hooks instance, not a plain
      // `{ postprocess }` object. ngx-markdown passes markedOptions straight to
      // marked.parse(text, options), and marked v15 there calls every hook
      // method (provideLexer, preprocess, processAllTokens, ...) on the object;
      // a plain object missing them throws "opt.hooks.provideLexer is not a
      // function" on EVERY message, rendering them all empty. A Hooks instance
      // carries the defaults and we override only postprocess.
      hooks: buildSanitizingHooks(),
    },
  }
}

function buildSanitizingHooks(): Hooks {
  const hooks = new Hooks();
  hooks.postprocess = (html: string) => sanitizeFeedHtml(html);
  return hooks;
}