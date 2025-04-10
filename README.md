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

## הוראות שימוש
לאחר ההרצה הראשונה, יש להכנס למערכת ולהזדהות כמנהל.  
יש לגשת לקישור:  
https://example.com/login  
ולהזדהות באמצעות הפרטים שהגדרתם בקובץ env.  
יש להגדיר את שם הערוץ תיאור הערוץ ולהעלות לוגו, שמירה וניתן להתחיל לפרסם הודעות...

## יבוא הודעות
ניתן לייבא הודעות באמצעות API כדי להוסיף תכנים מפלטפורמות חיצוניות, כולל אפשרות להגדיר תאריך יצירה מדויק (timestamp) עבור כל הודעה.

###  כתובת:
POST https://example.com/api/import/post

###  כותרות (Headers):
| שם הכותרת     | ערך                                    |
|----------------|------------------------------------------|
| Content-Type   | application/json                         |
| X-API-Key      | *המפתח שהוגדר במשתנה הסביבה `API_SECRET_KEY`* |

###  גוף הבקשה (Request Body):

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
כדי להפעיל את הוובהוק, יש להגדיר את משתני הסביבה הבאים בקובץ `.env`:

```
WEBHOOK_URL=https://example.com/webhook
WEBHOOK_VERIFY_TOKEN=your-secret-token  # Not required
```

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
אם הגדרתם `WEBHOOK_VERIFY_TOKEN`, תוכלו להשתמש בו כדי לוודא שהבקשות מגיעות אכן מהמערכת שלכם. בדקו שהערך ב-`verifyToken` תואם לערך שהגדרתם.
