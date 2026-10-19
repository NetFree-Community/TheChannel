/**
 * Turns a failed Magnet stats request into Hebrew text — the platform's
 * (GET /api/super-admin/magnet/stats) or an unlocked channel's own
 * (GET /api/channel/<slug>/admin/magnet/stats), both drawn by
 * app-magnet-stats-panel. Screens used to read the status on their own, and a 401
 * meant "no permission" there although it now means Magnet rejected the key.
 *
 * The error code in the body decides first: the backend answers Magnet's 401
 * as 502 (a 401 would sign the super admin out through the session-expired
 * interceptor), so the status alone cannot tell a bad key from an outage.
 */
export function magnetStatsError(err: any): { message: string; notConfigured: boolean; keyProblem: boolean } {
  const status: number = err?.status ?? 0;
  const body = err?.error;
  const code = String(typeof body === 'string' ? body : (body?.error ?? ''));
  // keyProblem: the fix is on the "פרסומות מגנט" screen (enter or replace the
  // key), so a screen can offer a way there instead of "try again".
  const result = (message: string, notConfigured = false, keyProblem = false) =>
    ({ message, notConfigured, keyProblem: keyProblem || notConfigured });

  // A channel locked to the platform's Magnet settings: there is nothing of
  // its own to show, and no key to fix.
  if (code === 'magnet_locked') {
    return result('הערוץ נעול להגדרות המגנט של הנהלת המערכת, ולכן אין כאן נתוני מגנט.');
  }
  if (code === 'missing_stats_key') {
    return result('מפתח הסטטיסטיקה של מגנט עוד לא הוגדר', true);
  }
  // No status === 401 check: the backend never forwards a 401 here (see
  // above), so a rejected key is only ever recognisable by its code.
  if (code === 'invalid_key' || code === 'missing_key') {
    return result('מפתח הסטטיסטיקה שגוי או שבוטל במגנט', false, true);
  }
  // A 401 from Magnet's platform gate rather than from site-stats: a setup
  // fault (the function deployed with JWT verification on), not an outage.
  if (code === 'upstream_unauthorized') {
    return result('מגנט דחה את הבקשה — בדקו את מפתח הסטטיסטיקה ואת פריסת site-stats במגנט');
  }
  if (code === 'invalid_key_format') {
    return result('מפתח הסטטיסטיקה השמור אינו תקין — הזינו אותו מחדש במסך "פרסומות מגנט"', false, true);
  }
  switch (status) {
    // site_not_active, or the function not deployed yet.
    case 404: return result('האתר לא נמצא במערכת מגנט או שאינו מאושר.');
    case 403: return result('אין הרשאה לגשת לנתוני מגנט.');
    case 502: return result('מגנט לא ענה. נסו שוב בעוד רגע.');
    case 0: return result('שגיאת רשת בקריאה לשרת מגנט');
    default: return result('הנתונים ממגנט לא נטענו. נסו שוב בעוד רגע.');
  }
}
