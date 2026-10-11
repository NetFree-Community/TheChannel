export type SettingFieldType = 'boolean' | 'text' | 'number' | 'url' | 'textarea' | 'password';

export interface SettingFieldSchema {
  key: string;
  label: string;
  /** What the setting does for readers and writers, in plain Hebrew. */
  description?: string;
  type: SettingFieldType;
  placeholder?: string;
  default?: string | number | boolean;
  hideWhen?: { key: string; equals: any };
  // Rendered as the input's min/max (number) or minlength (password) — a
  // hint only; the server's validateSettings is what rejects the value.
  min?: number;
  max?: number;
  minLength?: number;
  /** A secret: shown masked with a reveal button and tagged as such. */
  sensitive?: boolean;
  /** A key the owner has to invent: offer a button that fills a random one. */
  generate?: boolean;
}

export interface SettingsCategorySchema {
  id: string;
  title: string;
  icon?: string;
  description?: string;
  /** 'warning' marks a group whose fields hand out access (keys, tokens). */
  tone?: 'warning';
  fields: SettingFieldSchema[];
}

// Grouped by what the owner is trying to do, not by where the backend keeps
// the value: files and their size together, every external key together.
export const SETTINGS_SCHEMA: SettingsCategorySchema[] = [
  {
    id: 'files',
    title: 'קבצים ותמונות',
    icon: 'attach-2-outline',
    description: 'מה מותר לצרף להודעה וכמה מקום זה תופס. המכסה הכוללת של הערוץ מופיעה במדור "אחסון".',
    fields: [
      {
        key: 'max_file_size',
        label: 'גודל מרבי לקובץ (במגה-בייט)',
        description: 'קובץ גדול מזה יידחה בהעלאה עם הודעה לכותב. בלי ערך — 100 MB. אי אפשר לעבור 512 MB, וערך גדול יותר פשוט לא ייקלט.',
        type: 'number',
        placeholder: '100',
        default: 100,
        min: 1,
        max: 512,
      },
      {
        key: 'tinypng_api_key',
        label: 'מפתח TinyPNG לכיווץ תמונות',
        description: 'כשיש מפתח, כל תמונה (PNG, JPEG, WebP) נדחסת לפני השמירה ותופסת הרבה פחות מקום — הקוראים לא ירגישו בהבדל. מפתח חינמי מקבלים באתר tinypng.com. לא חובה.',
        type: 'password',
        placeholder: 'המפתח מהאתר של TinyPNG',
        sensitive: true,
      },
    ],
  },
  {
    id: 'notifications',
    title: 'התראות לנייד ולדפדפן',
    icon: 'bell-outline',
    description: 'התראה קופצת אצל הקוראים על כל הודעה חדשה. הקורא עצמו מאשר קבלת התראות בלחיצה על הפעמון בכותרת הערוץ.',
    fields: [
      {
        key: 'on_notification',
        label: 'לשלוח התראה על כל הודעה חדשה',
        description: 'דלוק — מי שאישר התראות לערוץ מקבל התראה בכל פרסום. כבוי — כפתור הפעמון לא מופיע ואף התראה לא נשלחת. תשתית ההתראות מוגדרת על ידי הנהלת המערכת; אם היא לא הוגדרה, המתג לא ישנה דבר.',
        type: 'boolean',
      },
    ],
  },
  {
    id: 'ads',
    title: 'פרסומת במסגרת',
    icon: 'pricetags-outline',
    description: 'מסגרת פרסומת שמוצגת לצד הערוץ. הכנסת כתובת מדליקה אותה, ריקון השדה מכבה. פרסומות של מגנט ADS מוגדרות במדור נפרד.',
    fields: [
      {
        key: 'ad-iframe-src',
        label: 'כתובת עמוד הפרסומת',
        description: 'כתובת אינטרנט מלאה (https://…) של העמוד שיוצג במסגרת. השאירו ריק כדי לא להציג פרסומת.',
        type: 'url',
        placeholder: 'https://ad.example.com/banner.html',
      },
      {
        key: 'ad-iframe-width',
        label: 'רוחב המסגרת (פיקסלים)',
        description: 'רוחב מומלץ: 300.',
        type: 'number',
        placeholder: '300',
      },
    ],
  },
  {
    id: 'integrations',
    title: 'חיבור למערכות חיצוניות',
    icon: 'link-2-outline',
    tone: 'warning',
    description: 'למי שמחבר את הערוץ לתוכנה אחרת: פרסום הודעות מבחוץ, ועדכון שרת חיצוני על כל שינוי. אם אין לכם כזו — אפשר לדלג על המדור כולו.',
    fields: [
      {
        key: 'api_secret_key',
        label: 'מפתח לפרסום הודעות מבחוץ',
        description: 'מערכת חיצונית ששולחת את המפתח הזה יכולה לפרסם הודעות בערוץ בלי להתחבר. כל עוד השדה ריק — הדרך הזו סגורה. לפחות 16 תווים; שמרו עליו כמו על סיסמה והחליפו אותו אם דלף.',
        type: 'password',
        placeholder: 'מפתח סודי ארוך ואקראי',
        minLength: 16,
        sensitive: true,
        generate: true,
      },
      {
        key: 'webhook_url',
        label: 'כתובת לעדכון על שינויים (Webhook)',
        description: 'בכל פרסום, עריכה או מחיקה של הודעה נשלחת בקשה לכתובת הזו. חייבת להתחיל ב-http:// או https://. ריק — לא נשלח דבר.',
        type: 'url',
        placeholder: 'https://example.com/webhook',
      },
      {
        key: 'webhook_verify_token',
        label: 'מחרוזת אימות סודית לכתובת העדכון',
        description: 'מצורפת לכל בקשה שנשלחת לכתובת העדכון, כדי שהשרת שמקבל אותה יוכל לוודא שהיא באמת מכאן. מומלץ כשיש כתובת עדכון; מעתיקים את אותו ערך גם לצד השני.',
        generate: true,
        type: 'password',
        placeholder: 'your-secret-token',
        sensitive: true,
      },
    ],
  },
];

// The single boolean parser for stored setting values — the backend accepts
// booleans, numbers and strings, so every reader must handle all three.
export function toBool(v: any): boolean {
  if (typeof v === 'boolean') return v;
  if (typeof v === 'number') return v !== 0;
  if (typeof v === 'string') {
    const s = v.toLowerCase().trim();
    return s === '1' || s === 'true' || s === 'yes' || s === 'on';
  }
  return false;
}

// Keys that used to be stored on the channel but are no longer part of the form.
// They are still recognised so they are not shown as raw "extra settings", and
// they are dropped from the payload on the next save.
export const LEGACY_SETTING_KEYS = [
  // Moved to a per-user localStorage preference — the server never read it.
  'enter_sends_message',
  // Saved by an old owner UI but consumed by nothing on the backend.
  'magnet_api_key',
  // Managed by the super-admin channel-features screen, not channel settings.
  'require_auth',
  'require_auth_for_view_files',
  'count_views',
  'contact_us',
];

// Keys owned by the magnet-ads tab. They live on the same channel settings list,
// so the settings tab used to show them as raw "advanced" rows — including the
// whole <script> snippet in a one-line input — and tidying them there broke the
// magnet tab. They are hidden here and written back verbatim on save.
export const PASSTHROUGH_SETTING_KEYS = [
  'magnet_enabled',
  'magnet_snippet',
  'magnet_mode',
  'magnet_per_messages',
  'magnet_min_time_seconds',
  'magnet_per_seconds',
  'magnet_min_messages_since',
];

export function getAllKnownKeys(): Set<string> {
  const keys = new Set<string>();
  for (const cat of SETTINGS_SCHEMA) {
    for (const f of cat.fields) keys.add(f.key);
  }
  for (const k of LEGACY_SETTING_KEYS) keys.add(k);
  for (const k of PASSTHROUGH_SETTING_KEYS) keys.add(k);
  keys.add('regex-replace');
  return keys;
}
