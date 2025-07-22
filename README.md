# הערוץ
![Go](https://img.shields.io/badge/Go-1.22-blue?style=flat-square&logo=go)
![Angular](https://img.shields.io/badge/Angular-DD0031?style=flat&logo=angular&logoColor=white)
![Caddy](https://img.shields.io/badge/Caddy-00BFB3?style=flat&logo=caddy&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-2496ED?style=flat&logo=docker&logoColor=white)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

**פרויקט פשוט וקל להקמת ערוץ עדכונים**  
צד שרת מהיר וחזק כתוב ב Go,  
מסד נתונים תואם Redis,  
צד לקוח עם Angular,  
Caddy לניהול דומיין ויצירת תעודה עבור האתר.  
שימוש ב google-oauth2 לאימות וכניסה לחשבון.  
הפרויקט מופץ תחת רישיון GNU General Public License v3 (GPLv3).  
כל הזכויות שמורות.  

## הוראות הרצה  
ניתן להוריד את הפרויקט עם:  
`git clone https://github.com/NetFree-Community/TheChannel`  

יש ליצור קובץ `.env` בהתאם לדוגמא בקובץ `sample.env`.  
הפרויקט מגיע עם **Caddy** מובנה.  
יש להוסיף את הדומיין בתוך `Caddyfile` כך:  

```caddy
example.com {
  reverse_proxy backend:3000
}
```

מלבד הטיפול בבקשות והפניה ל Container המתאים, **Caddy** מטפל גם בהוספת תעודה לדומיין.  
כך שנותר רק להריץ `docker-compose up --build -d` וסיימנו!  

### הגשת קבצי הפרויקט
בברירת מחדל, השרת מגיש את קבצי הפרויקט מנתיב `/usr/share/ng` שנוצר במהלך בניית הפרויקט.  
במידה ותרצו לשנות זאת, ניתן לקבוע נתיב אחר לקבצים באמצעות משתנה הסביבה:
~~~
ROOT_STATIC_FOLDER=/path/files
~~~

## הוראות שימוש  
לאחר ההרצה הראשונה, יש להכנס למערכת עם חשבון שמוגדר כמנהל.  
הגדרת מנהלים המזוהים באמצעות כתובת המייל, תחת משתנה הסביבה:  
```
ADMIN_USERS=example@gmail.com,example1@gmail.com
```
יש לגשת לקישור:  
https://example.com/login  
ולהזדהות באמצעות חשבון גוגל. הערכים הדרושים נמצאים בקובץ env.  
יצירת חשבון והגדרת חשבון עבור הערוץ בגוגל, ניתן לראות דוגמא במדריך [זה](https://dev.to/idrisakintobi/a-step-by-step-guide-to-google-oauth2-authentication-with-javascript-and-bun-4he7).  

יש להגדיר את שם הערוץ תיאור הערוץ ולהעלות לוגו, שמירה וניתן להתחיל לפרסם הודעות...  
### חיוב הזדהות לצפייה בתכני הערוץ:
בברירת מחדל, אין חיוב הזדהות במערכת.  
בכדי לחייב הזדהות, יש להגדיר בממשק הניהול את הערך הבא:    
`require_auth`
עם הערך 1  

## יבוא הודעות  
ניתן לייבא הודעות באמצעות API כדי להוסיף תכנים מפלטפורמות חיצוניות, כולל אפשרות להגדיר תאריך יצירה מדויק (timestamp) עבור כל הודעה.  

### כתובת:  
POST https://example.com/api/import/post  

### כותרות (Headers):  
| שם הכותרת     | ערך                                    |  
|----------------|------------------------------------------|  
| Content-Type   | application/json                         |  
| X-API-Key      | *המפתח שהוגדר בהגדרות הערוץ תחת הערך: `api_secret_key`* |  

### גוף הבקשה (Request Body):  
יש לשלוח אובייקט JSON במבנה הבא:  

```json
{
  "text": "Hello from another platform!",
  "author": "John Doe",
  "timestamp": "2025-04-06T12:34:56Z"
}
```  

## וובהוק (Webhook)  
המערכת תומכת בשליחת וובהוק בעת יצירה, עדכון או מחיקה של הודעות. הוובהוק יישלח רק אם הוגדר URL לוובהוק במשתני הסביבה.  

### הגדרת וובהוק  
כדי להפעיל את הוובהוק, יש להגדיר את ההגדרות הבאות בממשק הניהול:  

`webhook_url` עם הערך לדוגמא: https://example.com/webhook  
`webhook_verify_token` your-secret-token # לא חובה, אך מומלץ.

### מבנה הנתונים שנשלחים בוובהוק  
הוובהוק נשלח כבקשת POST עם תוכן JSON במבנה הבא:  

```json
{
  "action": "create", // "create", "update", או "delete"
  "message": {
  "id": 123,
  "type": "text",
  "text": "message content",
  "author": "username",
  "timestamp": "2025-04-10T18:30:00Z",
  "lastEdit": "2025-04-10T18:35:00Z",
  "deleted": false,
  "views": 5
  },
  "timestamp": "2025-04-10T18:35:05Z",
  "verifyToken": "your-secret-token" // If defined
}
```  

### אבטחה  
אם הגדרתם `webhook_verify_token`, תוכלו להשתמש בו כדי לוודא שהבקשות מגיעות אכן מהמערכת שלכם. בדקו שהערך ב-`verifyToken` תואם לערך שהגדרתם.  

## הפעלת קבלת התראות מהערוץ בכל עת  

## הוספת אימוג'ים להודעות
יש להגדיר את האימוגים המורשים בממשק הניהול.  
ניתן להוסיף אימוגים להודעות רק לאחר הזדהות בערוץ, גם בערוצים שלא מוגדרים לדרוש זאת. 

## הוספת פרסומות  
בממשק הניהול יש להגדיר את 2 הערכים:  
* `ad-iframe-src` קישור HTML להטמעה.
* `ad-iframe-width` רוחב חלון הפרסומת בפיקסלים. רוחב מומלץ 300.

## החלפה אוטומטית של טקסטים בעת פרסום הודעות חדשות


## תרומת קוד  
מעוניינים לתרום לפרויקט?  
כל תרומה חשובה ומתקבלת בברכה.  
נשמח שתשימו לב להערות TODO.  
אנא הקפידו להצמד לספריות שנעשה בהם שימוש עד כה בפרויקט.  


## הבהרה
נטפרי מספקת את הפלטפורמה כשירות בלבד כפי שהיא, ואינה נושאת באחריות כלשהי למתן תמיכה טכנית או לטיב המוצר.
כל המלצה לגורם חיצוני לסיוע בהטמעה או בתפעול המערכת הינה בגדר המלצה בלבד ונטפרי אינה אחראית לטיב השירות שיינתן על ידי גורמים חיצוניים ואינה מתחייבת לספק מענה או פתרון לבעיות שעלולות להתעורר במהלך ההתקנה או השימוש במערכת.
