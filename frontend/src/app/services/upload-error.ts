/**
 * The upload endpoint answers each refusal with its own status, and the forms
 * used to fold every one of them into the same "שגיאה בהעלאת קובץ": a writer
 * whose channel was full could not tell whether to wait, delete something or
 * ask the owner.
 */
export function uploadErrorMessage(status: number | undefined): string {
  switch (status) {
    case 413: return 'הקובץ גדול מדי';
    case 507: return 'האחסון של הערוץ מלא. יש למחוק קבצים או להגדיל את המכסה';
    case 429: return 'יותר מדי העלאות בזמן קצר, נסו שוב בעוד רגע';
    case 403: return 'העלאת קבצים כבויה בערוץ זה';
    case 503: return 'השרת עמוס בהעלאות כרגע, נסו שוב בעוד רגע';
    default: return 'שגיאה בהעלאת קובץ';
  }
}
