# הגדרות 

### הגשת קבצי הפרויקט
בברירת מחדל, השרת מגיש את קבצי הפרויקט מנתיב `/usr/share/ng` שנוצר במהלך בניית הפרויקט.  
במידה ותרצו לשנות זאת, ניתן לקבוע נתיב אחר לקבצים באמצעות משתנה הסביבה:
~~~
ROOT_STATIC_FOLDER=/path/files
~~~

## כותרת מותאמת אישית לשיפור וקידום האתר בתוצאות חיפוש
יש להגדיר את הטקסט הרצוי בממשק הניהול תחת ההגדרה:  
`custom-title`

## חיוב הזדהות לגישה לערוץ:
בברירת מחדל, אין חיוב הזדהות במערכת.  
בכדי לחייב הזדהות, יש להגדיר בממשק הניהול את הערך הבא:    
`require_auth`
עם הערך 1  

## חיוב הזדהות לצפיה בקבצי תמונות וסרטונים בערוץ
ברירת מחדל, במידה ולא מוגדר חיוב הזדהות בערוץ הקבצים נגישים לכל.  
במידה ומעוניינים לאפשר צפיה בקבצים רק לרשומים, יש להגדיר בהגדרות הניהול:  
`require_auth_for_view_files` עם הערך 1.

## אישור טעינת האתר ב - i frame רק לדומיינים מאושרים
במידה וההגדרה `frame_ancestors_domains` מוגדרת בממשק הניהול עם רשימת דומיינים מופרדים ברווח, הטעינה של האתר ב - i frame מאתרים חיצוניים תוגבל לדומיינים האלו בלבד.

## הגדרת דומיינים מאושרים לגישה לכתובות המאובטחות של האתר
לתוספת אבטחה ניתן להגדיר את ההגדרה `validate_Origin` עם הערך 1 כדי שהאתר יסמוך רק על הדומיין המקורי של האתר לגישה לכתובות מאובטחות באתר למניעת התקפות CSRF. במידה וההגדרה הזו מופעלת ניתן להגדיר דומיינים מאושרים נוספים ע"י ההגדרה `allowed_origins` עם רשימת הדומיינים מופרדים ע"י פסיק.

## הפעלת כפתור צור קשר
במידה וההגדרה `contact_us` מוגדרת בממשק הניהול עם קישור להפניה, יוצג למשתמשים כפתור צור קשר המפנה לקישור.  

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

## הגבלת גודל קבצים להעלאה
ברירת מחדל מוגדר כי ניתן להעלות קבצים עד 100MB, ניתן לשנות זאת על ידי הגדרת הערך הרצוי בהגדרות הניהול:  
`max_file_size` עם הערך הרצוי בMB. לדוגמא `50` בכדי להגביל ל50 MB

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
  "last_edit": "2025-04-10T18:35:00Z",
  "deleted": false,
  "views": 5
  },
  "timestamp": "2025-04-10T18:35:05Z",
  "verifyToken": "your-secret-token" // If defined
}
```  

### אבטחה  
אם הגדרתם `webhook_verify_token`, תוכלו להשתמש בו כדי לוודא שהבקשות מגיעות אכן מהמערכת שלכם. בדקו שהערך ב-`verifyToken` תואם לערך שהגדרתם.  

## הוספת אימוג'ים להודעות
יש להגדיר את האימוגים המורשים בממשק הניהול.  
ניתן להוסיף אימוגים להודעות רק לאחר הזדהות בערוץ, גם בערוצים שלא מוגדרים לדרוש זאת. 

## הוספת פרסומות  
בממשק הניהול יש להגדיר את 2 הערכים:  
* `ad-iframe-src` קישור HTML להטמעה.
* `ad-iframe-width` רוחב חלון הפרסומת בפיקסלים. רוחב מומלץ 300.  
### פרסום הודעות פרסום כהודעות רגילות בערוץ
במידה ורוצים לחסום אפשרות תגובת צופים בהודעות, כגון במקרי פרסום.  
ניתן לסמן את ההודעה כפרסום, באמצעות לחצן המנעול בבר לחצני העריכה.

## החלפה אוטומטית של טקסטים בעת פרסום הודעות חדשות
ניתן להגדיר תחת הערך `regex-replace` סט רגקס#החלפה עבור טקסטים.  
ניתן להגדיר גם כמה הגדרות.  
לדוגמא, הדגשה אוטומטית של כותרת הודעה:  
הטקסט: `הודעה חשובה! ערוך חשוב!`  
ההגדרה: `(.*?\!)(.*)#**$1**$2`  
תוצאה: `**הודעה חשובה!** ערוץ חשוב!`  
וכתוצאה מכך, זה יראה בערוץ כך:  
**הודעה חשובה!** ערוץ חשוב!

## ספירת צפיות ורשומים לערוץ
בפרטי הערוץ מוצגים כמות הרשומים שהזדהו עם חשבון גוגל בערוץ.  
כמו כן, במידה ומגדירים בהגדרות ניהול את הערך:
`count_views` עם הערך 1, נרשם במערכת גם כמות הצפיות בכל הודעה והנתון מוצג בערוץ ליד כל הודעה.  

## הפעלת קבלת התראות מהערוץ בכל עת  
השתמשנו בשירות FCM של גוגל.  
פרטים על יצירת חשבון והגדרתו ניתן למצוא במדריכים רבים במרחבי המרשתת, לדוגמא: [כאן](https://dev.to/this-is-angular/push-notifications-in-angular-19-with-firebase-cloud-messaging-3o3a) ו [כאן](https://youtu.be/iz5arafmatc).  
בכדי להפעיל את השירות יש להגדיר בהגדרות הניהול:
את `on_notification` עם הערך `1`.  
|setting|מקור/הסבר|
|-|-|
|`vapid`|cloudmessaging > Web Push certificates > Key pair|
|`fcm_api_key`|general > SDK setup and configuration > apiKey|
|`fcm_auth_domain`|general > SDK setup and configuration > authDomain|
|`fcm_project_id`|general > SDK setup and configuration > projectId|
|`fcm_storage_bucket`|general > SDK setup and configuration > storageBucket|
|`fcm_messaging_sender_id`|general > SDK setup and configuration > messagingSenderId|
|`fcm_app_id`|general > SDK setup and configuration > appId|
|`fcm_measurement_id`|general > SDK setup and configuration > measurementId|
|`project_domain`|URL להפניית המשתמשים בלחיצה על התראה|

בכדי ששליחת ההודעות תעבוד, יש להוריד קובץ JSON עם מפתח פרטי.  
הקובץ זמין להורדה מ[כאן](https://console.firebase.google.com/), לאחר בחירת הפרויקט, תחת הלשונית:  
`serviceaccounts >  Generate new private key`  
יש להעתיק את הערכים ולהגדיר אותם בממשק הניהול, רשימת ההגדרות המלאה להלן:  
|setting|
|-------|
|`fcm_json_type`|
|`fcm_json_project_id`|
|`fcm_json_private_key_id`|
|`fcm_json_private_key`|
|`fcm_json_client_email`|
|`fcm_json_client_id`|
|`fcm_json_auth_uri`|
|`fcm_json_token_uri`|
|`fcm_json_auth_provider_x509_cert_url`|
|`fcm_json_client_x509_cert_url`|
|`fcm_json_universe_domain`|


## ריכוז הגדרות בממשק ניהול
|setting        |value | הסבר |
|---------------|------|------|
|`require_auth`   | `1`    |חיוב הזדהות בכניסה לערוץ |
|`require_auth_for_view_files`|`1`|חיוב הזדהות לצפיה בקבצי תמונות וסרטונים בערוץ|
|`validate_Origin`|`1`|בדיקה של הOrigin ממנו נשלחת הבקשה לדומיין המקורי של האתר או לדומיינים המוגדרים בהגדרה `allowed_origins`|
|`frame_ancestors_domains`|url|רשימת כתובות מופרדת ברווחים של דומיינים המאושרים לטעינת האתר ב - i frame|
|`api_secret_key`|`1`|מפתח עבור יבוא הודעות באמצעות API|
|`webhook_url`|`https://example.com/webhook`|כתובת לשליחת וובהוק|
|`webhook_verify_token`|`your-secret-token`|טוקן לשליחה יחד עם וובהוק|
|`ad-iframe-src`| |קישור HTML להטמעת פרסומת|
|`ad-iframe-width`|`300`|רוחב פרסום|
|`count_views`|`1`|הפעלת מונה צפיות פר הודעה|
|`regex-replace`|`(.*?\!)(.*)#**$1**$2`|ערך של רגקס והחלפה בכדי ליצור החלפות אוטומטיות לטקסטים|
|`on_notification`|`1`|הפעלת התראות דחיפה|
|`max_file_size`|`50`|הגבלת משקל קבצים|
|`custom_title`||title מותאם אישית|
|`contact_us`|url|הפעלת כפתור צור קשר|