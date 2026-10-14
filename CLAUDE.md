# מערכת הערוצים (fork)

> קובץ קצר בכוונה, כי המאגר הזה ציבורי. ההקשר של הפרויקט נמצא אצל הבעלים, במאגר פרטי.

- **מיזוג ל-`master` הוא פריסה.** CapRover בונה ופורס לבד כל push ל-`master`, בלי שלב נוסף. לכן כל שינוי נכנס ב-PR,
  והמיזוג רק אחרי אישור מפורש של בעל המאגר. אחרי מיזוג מוודאים מול האתר החי שהגרסה החדשה עלתה, ולא מניחים.
- **תשובה של מנהל מוצגת תמיד "ניהול"**, אף פעם לא שם או חשבון התחברות. כלל קבוע של הבעלים.
- טקסטים למשתמש בעברית ו-RTL.

## בנייה ובדיקות

```bash
# Backend (Go): needs a local Redis
redis-server --port 6379 --daemonize yes
cd backend && GOFLAGS=-mod=mod GOTOOLCHAIN=auto REDIS_ADDR=127.0.0.1:6379 go test ./...

# Frontend (Angular)
cd frontend && npx ng build --configuration production
```

אין CI במאגר, ולכן מריצים את שתיהן לפני כל push.
