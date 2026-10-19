# הערוץ
![Go](https://img.shields.io/badge/Go-1.25-blue?style=flat-square&logo=go)
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
  reverse_proxy backend:3000 {
    header_up X-Real-IP {remote_host}
    header_up -True-Client-IP
  }
}
```

שתי שורות ה-`header_up` הן חובה, כמו בקובץ `Caddyfile` שמגיע עם הפרויקט: השרת סומך על הכותרת `X-Real-IP` כדי לזהות את כתובת הלקוח (הגבלת קצב לפי IP ורישום ללוג), ולכן היא חייבת להיקבע על ידי ה-proxy ולא להגיע מהלקוח.  
מלבד הטיפול בבקשות והפניה ל Container המתאים, **Caddy** מטפל גם בהוספת תעודה לדומיין.  
כך שנותר רק להריץ `docker-compose up --build -d`.  

## הוראות שימוש  
לאחר ההרצה הראשונה, יש להכנס למערכת עם חשבון שמוגדר כמנהל-על.  
הגדרת מנהלי-על המזוהים באמצעות כתובת המייל, תחת משתנה הסביבה:  
```
ADMIN_USERS=example@gmail.com,example1@gmail.com
```
יש לגשת לקישור:  
https://example.com/login  
ולהזדהות באמצעות חשבון גוגל. הערכים הדרושים נמצאים בקובץ env.  
יצירת חשבון והגדרת חשבון עבור הערוץ בגוגל, ניתן לראות דוגמא במדריך [זה](https://dev.to/idrisakintobi/a-step-by-step-guide-to-google-oauth2-authentication-with-javascript-and-bun-4he7).  

מנהל-על מועבר אחרי הכניסה לפאנל מנהל-העל (`/super-admin`). שם, תחת **ערוצים** ← **ערוץ חדש**, יוצרים את הערוץ הראשון ובוחרים לו מזהה (slug).  
הערוץ נפתח בכתובת `https://example.com/channel/<slug>`. בתוך הערוץ: תפריט המשתמש ← **ניהול הערוץ** ← **פרטי הערוץ** — מגדירים שם, תיאור ולוגו, שומרים, וניתן להתחיל לפרסם הודעות.  
משתמש רגיל לוחץ בדף הבית על "פתיחת ערוץ" — הלחיצה מעבירה ישירות לכניסה עם גוגל — ומגיע לעמוד **הערוצים שלי** (`/channel`): משם פותחים ערוץ משלו תוך רגע (עד 5 ערוצים לחשבון), עוברים בין הערוצים, משתפים את הקישור ומגיעים למסך הניהול של כל ערוץ (`/channel/<slug>/manage`). פירוט ב-[SET.md](SET.md).  

## הגדרות נוספות
[שאר ההגדרות, הוראות מפורטות ועוד נמצאים כאן](SET.md).

## תרומת קוד  
מעוניינים לתרום לפרויקט?  
כל תרומה חשובה ומתקבלת בברכה.  
נשמח שתשימו לב להערות TODO.  
אנא הקפידו להצמד לספריות שנעשה בהם שימוש עד כה בפרויקט.  