package main

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
)

// The privacy policy and the terms of service. Google's OAuth consent screen
// links to both, so they must answer without a session and without the app:
// each is a complete document rendered here, like the content pages.
//
// Like the content pages, every sentence must be true of the code as it
// stands: what auth.go asks Google for, what the session cookie holds, what
// notifications.go and the ad slot store. When one of those changes, the text
// changes with it, and the page's Revised date is bumped.

type legalPage struct {
	Path        string
	Title       string
	Description string
	Revised     string
	Body        *template.Template
}

var legalPages = []legalPage{
	{
		Path:        "/privacy",
		Title:       "מדיניות פרטיות",
		Description: "איזה מידע נאסף בשירות, למה, עם מי הוא משותף ואיך מבקשים למחוק אותו.",
		Revised:     "22.10.2026",
		Body: template.Must(template.New("privacy").Parse(`
<p>{{.SiteName}} ({{.Origin}}) הוא שירות לפתיחת ערוצי עדכונים, המופעל על ידי מגנט (magnetads.co.il). בעמוד הזה מוסבר איזה מידע נאסף כשמשתמשים בשירות, למה, ומה אפשר לעשות איתו.</p>

<h2>קריאה בערוץ בלי להתחבר</h2>
<p>אפשר לקרוא ערוץ ציבורי בלי חשבון. במקרה כזה לא נאסף מידע מזהה. הדפדפן שומר אצלכם בלבד כמה העדפות טכניות, למשל איזו הודעה קראתם אחרונה בכל ערוץ.</p>

<h2>התחברות עם חשבון גוגל</h2>
<p>ההתחברות נעשית רק דרך חשבון גוגל. אנחנו מבקשים מגוגל את השם, כתובת האימייל ותמונת הפרופיל שלכם, ולא שום הרשאה אחרת. אין לנו גישה לסיסמה, לתיבת הדואר, לאנשי הקשר או לקבצים שלכם בגוגל, ואנחנו לא מפרסמים בשמכם.</p>
<p>המידע הזה משמש כדי:</p>
<ul>
<li>לרשום את הערוצים שפתחתם על שמכם ולזהות אתכם בכניסה הבאה;</li>
<li>לתת לכם את ההרשאות שקיבלתם בערוץ (כותב, מנהל או בעלים);</li>
<li>לשלוח לכם הודעות שקשורות לשירות, כמו אישור על פתיחת ערוץ או תשובה לפנייה שלכם.</li>
</ul>
<p>לפני פתיחת ערוץ מבקשים מכם לאשר את תנאי השימוש ואת המדיניות הזו. אנחנו שומרים את האישור עם כתובת האימייל, השם ותאריך האישור.</p>

<h2>דיוור</h2>
<p>באותו שלב אפשר גם לבחור לקבל מאיתנו במייל עדכונים, טיפים והצעות על השירות ועל מגנט. זו בחירה נפרדת ולא חובה: הערוץ נפתח גם בלעדיה. אם הסכמתם, נשמרים כתובת האימייל, השם ומועד ההסכמה, והם משמשים לדיוור הזה בלבד. אפשר להסיר את עצמכם בכל עת, בקישור ההסרה שבכל מייל או בפנייה אלינו, וההסרה לא משפיעה על הערוץ.</p>
<p>אחרי ההתחברות נשמרת בדפדפן עוגייה (cookie) מאובטחת שמחזיקה את החיבור שלכם עד 30 יום, או עד שתתנתקו.</p>

<h2>מה נשמר כשאתם משתמשים בשירות</h2>
<ul>
<li>הודעות, תמונות, סרטונים וקבצים שמנהלי ערוץ מפרסמים בו.</li>
<li>תגובות אימוג׳י שמשתמשים מחוברים משאירים על הודעות.</li>
<li>דיווחים ופניות לתמיכה, יחד עם כתובת האימייל של הפונה.</li>
<li>אם ביקשתם לקבל התראות, מזהה טכני של הדפדפן או המכשיר שנדרש כדי לשלוח אליו התראה. אפשר לבטל את ההתראות בכל עת.</li>
</ul>

<h2>פרסומות</h2>
<p>בחלק מהערוצים מוצגות פרסומות ממערכת הפרסום של מגנט. כדי לא להציג לכם את אותה פרסומת שוב ושוב, נשמר בדפדפן מזהה צפייה אקראי שמתחלף כל 24 שעות. המזהה הזה לא קשור לשם, לאימייל או לחשבון שלכם.</p>

<h2>עם מי המידע משותף</h2>
<p>אנחנו לא מוכרים מידע אישי ולא מעבירים אותו לצד שלישי לצורכי שיווק. המידע עובר רק לספקים שמפעילים את השירות בשבילנו:</p>
<ul>
<li>Google: ההתחברות, ושליחת התראות דרך Firebase Cloud Messaging;</li>
<li>Cloudflare: אחסון הקבצים שמועלים לערוצים;</li>
<li>מגנט: שליחת הודעות האימייל של השירות והצגת הפרסומות.</li>
</ul>
<p>מנהלי ערוץ רואים את כתובות האימייל של מי שקיבל הרשאה בערוץ שלהם. נמסור מידע לרשויות רק אם נחויב לכך על פי דין.</p>

<h2>השימוש במידע מגוגל</h2>
<p>השימוש במידע שמתקבל מ-Google APIs והעברתו לכל אפליקציה אחרת יעמדו ב<a href="https://developers.google.com/terms/api-services-user-data-policy" rel="noopener">Google API Services User Data Policy</a>, כולל דרישות ה-Limited Use.</p>

<h2>אבטחה ושמירת מידע</h2>
<p>החיבור לאתר מוצפן (HTTPS), והגישה לכל ערוץ מוגבלת לפי ההרשאות שבו. המידע נשמר כל עוד החשבון או הערוץ פעילים, או עד שתבקשו למחוק אותו.</p>

<h2>הזכויות שלכם</h2>
<p>אתם יכולים לבקש לעיין במידע שנשמר עליכם, לתקן אותו או למחוק אותו, כולל מחיקת החשבון והערוצים שפתחתם. אפשר גם לבטל בכל עת את הגישה של השירות לחשבון הגוגל שלכם, בדף <a href="https://myaccount.google.com/permissions" rel="noopener">ההרשאות של חשבון Google</a>.</p>

<h2>יצירת קשר</h2>
<p>לכל שאלה או בקשה בנושא פרטיות, פנו אלינו דרך <a href="/#contact">טופס יצירת הקשר</a> באתר.</p>

<h2>שינויים במדיניות</h2>
<p>אם המדיניות תשתנה, הגרסה המעודכנת תפורסם בעמוד הזה עם תאריך העדכון.</p>
`)),
	},
	{
		Path:        "/terms",
		Title:       "תנאי שימוש",
		Description: "התנאים לשימוש בשירות: מה מותר לפרסם, האחריות על התוכן, הסרת תוכן והגבלת אחריות.",
		Revised:     "22.10.2026",
		Body: template.Must(template.New("terms").Parse(`
<p>תנאים אלה חלים על השימוש ב{{.SiteName}} ({{.Origin}}), שירות לפתיחת ערוצי עדכונים המופעל על ידי מגנט (magnetads.co.il). השימוש בשירות, בקריאה או בפתיחת ערוץ, מהווה הסכמה לתנאים, ולפני פתיחת ערוץ מתבקשים לאשר אותם במפורש. אם אינכם מסכימים להם, אל תשתמשו בשירות.</p>

<h2>השירות</h2>
<p>השירות מאפשר לפתוח ערוץ, לפרסם בו הודעות, תמונות, סרטונים וקבצים, ולשתף אותו בקישור. הקריאה בערוץ ציבורי פתוחה לכל אחד. פתיחת ערוץ, ניהולו ותגובות דורשים התחברות עם חשבון גוגל. השימוש הבסיסי בשירות ניתן בחינם.</p>

<h2>החשבון שלכם</h2>
<ul>
<li>אתם אחראים לכל מה שנעשה בחשבון שלכם ובערוצים שפתחתם, כולל פעולות של מי שצירפתם לצוות הערוץ.</li>
<li>שמרו על הגישה לחשבון הגוגל שלכם. אם אתם חושדים בשימוש לא מורשה, פנו אלינו.</li>
</ul>

<h2>תוכן שאתם מפרסמים</h2>
<p>התוכן שאתם מפרסמים שייך לכם, והאחריות עליו היא שלכם בלבד. כדי להפעיל את השירות אתם מעניקים לנו רשות לאחסן את התוכן, להציג אותו לקוראי הערוץ ולהפיץ אותו כפי שהגדרתם (למשל בהתראות או בתוצאות חיפוש של ערוץ ציבורי).</p>
<p>אסור לפרסם בשירות תוכן ש:</p>
<ul>
<li>אינו חוקי, או מפר זכויות יוצרים, סימני מסחר או זכויות אחרות של צד שלישי;</li>
<li>פוגעני, מאיים, מסית, משמיץ או פוגע בפרטיות של אחרים;</li>
<li>מכיל תוכן מיני בוטה, או פוגע בקטינים בכל דרך;</li>
<li>מכיל ספאם, הונאה, התחזות, וירוסים או קוד זדוני.</li>
</ul>

<h2>שימוש אסור בשירות</h2>
<p>אין לנסות לעקוף את מנגנוני האבטחה וההרשאות, להעמיס על השירות, לאסוף מידע ממנו באופן אוטומטי בניגוד להגבלות שבו, או להשתמש בו בדרך שפוגעת במשתמשים אחרים או בשירות עצמו.</p>

<h2>הסרת תוכן והשעיה</h2>
<p>אנחנו רשאים להסיר תוכן, לחסום ערוץ או להשעות חשבון שמפרים את התנאים או את הדין, או בעקבות דיווח, גם בלי הודעה מוקדמת. מצאתם תוכן שמפר את התנאים? אפשר לדווח עליו מתוך הערוץ או דרך טופס יצירת הקשר.</p>

<h2>פרסומות</h2>
<p>בחלק מהערוצים יכולות להופיע פרסומות ממערכת הפרסום של מגנט. התוכן של פרסומת הוא באחריות המפרסם.</p>

<h2>פרטיות</h2>
<p>האופן שבו אנחנו אוספים ומשתמשים במידע מוסבר ב<a href="/privacy">מדיניות הפרטיות</a>, שהיא חלק מהתנאים האלה.</p>

<h2>הגבלת אחריות</h2>
<p>השירות ניתן כמות שהוא (AS IS). אנחנו עושים מאמץ שהוא יפעל ברציפות ושהמידע בו יישמר, אבל לא מתחייבים שהשירות יהיה זמין תמיד או נקי מתקלות, ולא נישא באחריות לנזק שנגרם מהשימוש בו, מהפסקה בפעילותו, מאובדן תוכן או מתוכן שפרסמו משתמשים אחרים, ככל שהדין מתיר. מומלץ לשמור עותק של תוכן חשוב.</p>

<h2>הפסקת שימוש</h2>
<p>אתם יכולים להפסיק להשתמש בשירות בכל עת ולבקש למחוק את החשבון והערוצים שלכם. אנחנו רשאים לשנות את השירות או להפסיק אותו, כולו או חלקו.</p>

<h2>שינויים בתנאים</h2>
<p>אם התנאים ישתנו, הגרסה המעודכנת תפורסם בעמוד הזה עם תאריך העדכון. המשך השימוש בשירות אחרי העדכון מהווה הסכמה לתנאים המעודכנים.</p>

<h2>דין ושיפוט</h2>
<p>על התנאים האלה חל הדין הישראלי, וסמכות השיפוט הבלעדית נתונה לבתי המשפט המוסמכים בישראל.</p>

<h2>יצירת קשר</h2>
<p>לכל שאלה על התנאים, פנו אלינו דרך <a href="/#contact">טופס יצירת הקשר</a> באתר.</p>
`)),
	},
}

var legalShellTmpl = template.Must(template.New("legal").Parse(`<!doctype html>
<html lang="he" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Page.Title}} · {{.SiteName}}</title>
<meta name="description" content="{{.Page.Description}}">
<link rel="canonical" href="{{.Origin}}{{.Page.Path}}">
<meta name="theme-color" content="#5a5ff5">
<link rel="icon" type="image/x-icon" href="/assets/favicon.ico">
<link rel="icon" type="image/svg+xml" href="/assets/icon.svg">
<style>
:root{--ink:#1f2433;--muted:#5b6275;--brand-ink:#3e42c9;--bg:#f6f7fb;--card:#fff;--line:#e3e6ef}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font-family:Assistant,system-ui,-apple-system,"Segoe UI",sans-serif;font-size:1.0625rem;line-height:1.7}
a{color:var(--brand-ink)}
.top{padding:.75rem 1.25rem;background:var(--card);border-bottom:1px solid var(--line)}
.brand{font-weight:800;font-size:1.2rem;color:var(--ink);text-decoration:none}
.wrap{max-width:760px;margin:0 auto;padding:1rem 1.25rem 2.5rem}
h1{font-size:clamp(1.75rem,5vw,2.2rem);font-weight:800;line-height:1.2;margin:.75rem 0 .25rem}
.date{color:var(--muted);margin:0 0 1.5rem}
h2{font-size:1.25rem;font-weight:800;margin:1.75rem 0 .5rem}
p{margin:0 0 .85rem}
ul{padding-inline-start:1.4rem;margin:0 0 1rem}
li{margin-bottom:.4rem}
footer{padding:1.5rem 1.25rem;text-align:center;font-size:.9rem;color:var(--muted);border-top:1px solid var(--line);background:var(--card)}
footer a{margin:0 .5rem}
</style>
</head>
<body>
<header class="top"><a class="brand" href="/">{{.SiteName}}</a></header>
<main class="wrap">
<h1>{{.Page.Title}}</h1>
<p class="date">עודכן לאחרונה: {{.Page.Revised}}</p>
{{.Body}}</main>
<footer><a href="/privacy">מדיניות פרטיות</a><a href="/terms">תנאי שימוש</a><a href="/#contact">יצירת קשר</a></footer>
</body>
</html>
`))

func serveLegalPage(w http.ResponseWriter, r *http.Request, p *legalPage) {
	if r.URL.Path != p.Path {
		// One URL per page: "/privacy/" would otherwise be a duplicate.
		http.Redirect(w, r, p.Path, http.StatusMovedPermanently)
		return
	}
	cfg := getGlobalConfig()
	data := struct {
		SiteName string
		Origin   string
	}{siteNameOf(cfg), requestOrigin(r, cfg)}

	var body, out bytes.Buffer
	err := p.Body.Execute(&body, data)
	if err == nil {
		err = legalShellTmpl.Execute(&out, struct {
			SiteName string
			Origin   string
			Page     *legalPage
			Body     template.HTML
		}{data.SiteName, data.Origin, p, template.HTML(body.String())})
	}
	if err != nil {
		log.Printf("legal page %s: %v\n", p.Path, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(out.Bytes())
}
