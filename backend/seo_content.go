package main

import (
	"bytes"
	"html"
	"html/template"
	"log"
	"net/http"
	"strings"
)

// Every sentence here is a claim about the product that a search result will
// show to strangers, so each must be true of the code as it stands: no
// statement about filtering services, no user counts, no ratings. When a
// feature changes, the text changes with it.

// homeTitle is the home page's <title> before the site name. The landing
// component sets the same string at runtime (landing-page.component.ts), so
// in-app navigation back to "/" does not change the tab.
const homeTitle = "פתיחת ערוץ עדכונים בחינם, בלי אפליקציה"

const homeDescription = "פתחו ערוץ עדכונים בחינם, תוך דקה: חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל. הודעות, תמונות וסרטונים לקהילה או לעסק, בקישור מהדפדפן, בלי אפליקציה."

// contentRevised is the lastmod of the server-rendered pages in the sitemap.
// Bump it when their text changes.
const contentRevised = "2026-10-14"

type faqItem struct {
	Q string
	A string
}

// homeFAQ is the single source of the home page's questions: rendered into
// the static HTML, into the FAQPage structured data, and read back from that
// structured data by the Angular landing page, so the three cannot drift.
var homeFAQ = []faqItem{
	{
		Q: "איך פותחים ערוץ עדכונים?",
		A: "מתחברים עם חשבון גוגל, נותנים לערוץ שם, והוא נפתח מיד עם קישור משלו. אין טפסים ואין המתנה לאישור.",
	},
	{
		Q: "כמה עולה לפתוח ערוץ?",
		A: "פתיחת הערוץ והשימוש בו בחינם.",
	},
	{
		Q: "הקוראים צריכים להתקין אפליקציה או להירשם?",
		A: "לא. הערוץ נפתח בדפדפן, בטלפון ובמחשב. בערוץ ציבורי מספיק הקישור כדי לקרוא, בלי חשבון ובלי מספר טלפון.",
	},
	{
		Q: "מי יכול לכתוב בערוץ?",
		A: "רק אתם ומי שצירפתם לצוות הערוץ, כל אחד עם ההרשאה שלו: כותב, מנהל או בעלים. הקוראים לא כותבים בערוץ, ומי שמחובר יכול להגיב באימוג׳י.",
	},
	{
		Q: "איך הקוראים יודעים שפורסמה הודעה חדשה?",
		A: "מי שהערוץ פתוח אצלו רואה כל הודעה חדשה מיד, בלי לרענן. כשהתראות פעילות בערוץ, קורא שמתחבר לחשבון יכול לבקש לקבל התראה לנייד על כל פרסום.",
	},
	{
		Q: "אפשר לפרסם תמונות, סרטונים וקבצים?",
		A: "כן. מעלים תמונות, סרטונים וקבצים ישירות לערוץ, וקישור ליוטיוב מתנגן בתוך ההודעה.",
	},
	{
		Q: "אפשר לכתוב הודעה עכשיו ולפרסם אותה אחר כך?",
		A: "כן. כותבים את ההודעה, בוחרים מועד, והערוץ מפרסם אותה לבד בזמן שנקבע.",
	},
	{
		Q: "צריך אפליקציה כדי לנהל את הערוץ?",
		A: "לא. גם הכתיבה והניהול נעשים מהדפדפן, מהטלפון או מהמחשב.",
	},
	// The two below are appended, not inserted: the guide pages pick
	// questions from this list by position.
	{
		Q: "זו חלופה לערוץ וואטסאפ או לקבוצת צ׳אט במייל?",
		A: "כן, כשצריך להעביר עדכונים לקהל. כמו בערוץ וואטסאפ רק אתם כותבים, אבל הקוראים לא צריכים וואטסאפ, אפליקציה או מספר טלפון, רק דפדפן. ובניגוד לקבוצה במייל אין שרשורי תשובות לכולם: ההודעות מחכות בערוץ לפי הסדר. הן לא נשלחות למייל; הקוראים נכנסים לקישור, ומי שמחובר יכול לבקש התראות לנייד כשהן פעילות בערוץ.",
	},
	{
		Q: "הערוץ פתוח בנטפרי?",
		A: "האתר עצמו פתוח בנטפרי, אבל כל ערוץ צריך אישור נפרד מנטפרי לפני שגולשי נטפרי יכולים לקרוא אותו. פונים לנטפרי בבקשה לפתוח את הכתובת המלאה של הערוץ, ואפשר לבקש באותה פנייה גם את פתיחת התמונות והקבצים שבו.",
	},
}

// faqWhatsApp and faqNetFree name the appended questions for the pages that
// quote them.
var (
	faqWhatsApp = homeFAQ[8]
	faqNetFree  = homeFAQ[9]
)

type contentSection struct {
	Heading    string
	Paragraphs []string
	List       []string
	Ordered    bool
	// ChannelURLExample adds, after the list, the shape of a channel's full
	// address on this deployment (its own domain, which the text cannot know).
	ChannelURLExample bool
}

// contentPage is a page rendered entirely on the server: an explanation of
// one thing people search for, readable without any script.
type contentPage struct {
	Path        string
	Title       string
	H1          string
	Description string
	Lead        string
	Sections    []contentSection
	FAQ         []faqItem
}

var contentPages = []contentPage{
	{
		Path:        "/open-channel",
		Title:       "פתיחת ערוץ: מדריך לפתיחת ערוץ עדכונים בחינם",
		H1:          "פתיחת ערוץ עדכונים, צעד אחר צעד",
		Description: "איך פותחים ערוץ עדכונים בחינם תוך דקה: מתחברים עם גוגל, בוחרים שם ומשתפים קישור. מה צריך להכין, איך בוחרים כתובת ואיך מביאים קוראים.",
		Lead:        "ערוץ הוא דף אחד שבו אתם מפרסמים הודעות, תמונות, סרטונים וקישורים, וכל מי שיש לו את הקישור רואה אותם. כך פותחים אחד.",
		Sections: []contentSection{
			{
				Heading: "שלושה צעדים",
				Ordered: true,
				List: []string{
					"מתחברים עם חשבון גוגל. בלי סיסמה חדשה ובלי טופס הרשמה; החשבון רק רושם את הערוץ על שמכם.",
					"נותנים לערוץ שם. הכתובת נגזרת מהשם (שם בעברית מתועתק לאותיות לטיניות), אפשר לשנות אותה, והמערכת בודקת שהיא פנויה.",
					"משתפים את הקישור. שולחים אותו בוואטסאפ, במייל או בכל דרך אחרת, וכל מי שפותח אותו רואה את הערוץ.",
				},
			},
			{
				Heading: "מה כדאי להכין מראש",
				List: []string{
					"שם קצר וברור: הוא מופיע בראש הערוץ, בלשונית הדפדפן ובתצוגה המקדימה כשמשתפים את הקישור.",
					"תיאור של משפט או שניים: במה הערוץ עוסק ולמי הוא מיועד. הוא מוצג מתחת לשם ובתצוגה המקדימה של הקישור.",
					"לוגו או תמונה מרובעת, שתזהה את הערוץ אצל הקוראים.",
					"קישור ליצירת קשר, אם תרצו שקוראים יוכלו לפנות אליכם.",
				},
			},
			{
				Heading: "אחרי שהערוץ נפתח",
				Paragraphs: []string{
					"כותבים הודעה ראשונה מתוך הערוץ עצמו. אפשר לצרף תמונות, סרטונים וקבצים, לעצב טקסט, ולתזמן הודעה שתתפרסם מאוחר יותר.",
					"אם אחרים צריכים לכתוב או לנהל איתכם, מצרפים אותם לצוות לפי כתובת המייל שלהם, כל אחד בהרשאה שמתאימה לו: כותב, מנהל או בעלים.",
				},
			},
			{
				Heading: "איך מביאים קוראים",
				Paragraphs: []string{
					"הקוראים לא צריכים להתקין דבר. מספיק שיפתחו את הקישור, בטלפון או במחשב, כדי לראות את כל ההודעות ואת כל הודעה חדשה ברגע שהיא מתפרסמת.",
					"כדאי לשים את הקישור במקום שאנשים כבר מסתכלים בו: בהודעה בקבוצה קיימת, בחתימת המייל, באתר או בעלון.",
				},
			},
		},
		FAQ: []faqItem{homeFAQ[1], homeFAQ[2], homeFAQ[3], homeFAQ[7]},
	},
	{
		Path:        "/community-channel",
		Title:       "ערוץ הודעות לקהילה, לבית ספר ולעסק, בלי אפליקציה",
		H1:          "ערוץ הודעות לקהילה, לבית הספר או לעסק",
		Description: "ערוץ עדכונים חד-כיווני בדפדפן, חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל: אתם מפרסמים, והקהל קורא מקישור אחד, בלי אפליקציה ובלי מספר טלפון.",
		Lead:        "כשצריך להעביר עדכונים לקהל, קבוצה שכולם כותבים בה מהר מאוד מתמלאת. ערוץ הוא הכיוון ההפוך: אתם מפרסמים, והקהל קורא.",
		Sections: []contentSection{
			{
				Heading: "ערוץ, לא קבוצה",
				Paragraphs: []string{
					"בערוץ רק אתם ומי שצירפתם לצוות כותבים. ההודעות לא נקברות בין תגובות, וקורא שמצטרף מאוחר רואה גם את מה שפורסם לפניו.",
					"הקוראים לא כותבים בערוץ. מי שמחובר יכול להגיב באימוג׳י, כך שרואים מה עניין את הקהל בלי שרשור של תשובות.",
				},
			},
			{
				Heading: "גם למי שאין אפליקציה",
				Paragraphs: []string{
					"הערוץ הוא אתר: נפתח בכל דפדפן, בטלפון ובמחשב. בערוץ ציבורי הקורא לא צריך חשבון, מספר טלפון או אפליקציה מסוימת, ולכן אפשר להגיע גם למי שאינו משתמש ברשת חברתית או באפליקציית מסרים.",
					"לכן ערוץ משמש חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל: מי שיש לו וואטסאפ, מי שקורא רק במחשב ומי שגולש עם סינון מגיעים כולם לאותו קישור. האתר פתוח בנטפרי, וכל ערוץ נפתח לגולשי נטפרי אחרי אישור נפרד מהם.",
				},
			},
			{
				Heading: "למי זה מתאים",
				List: []string{
					"קהילות ושכונות: הודעות ועד, אירועים, שינויים בלוח הזמנים.",
					"בתי ספר וגנים: עדכונים להורים, תזכורות ותמונות מפעילויות.",
					"בתי כנסת ומוסדות: זמנים, הודעות והכרזות.",
					"עסקים: מבצעים, שעות פתיחה ועדכונים ללקוחות.",
					"ארגונים ומתנדבים: קריאה לפעולה ועדכון שוטף של הפעילים.",
					"משפחה וחברים: תמונות ועדכונים במקום אחד.",
				},
			},
			{
				Heading: "מה יש בערוץ",
				List: []string{
					"עדכונים חיים: הודעה שפורסמה מופיעה אצל הקוראים באותו רגע, בלי לרענן.",
					"תמונות, סרטונים וקבצים, וסרטוני יוטיוב שמתנגנים בתוך ההודעה.",
					"הודעות מתוזמנות שמתפרסמות לבד במועד שנקבע.",
					"צוות כותבים ומנהלים, כל אחד עם ההרשאה שלו.",
					"התראות לנייד לקוראים מחוברים שמבקשים אותן, כשהן פעילות בערוץ.",
					"ההודעות מוצגות לקוראים בשם הערוץ, לא בשם מי שכתב אותן.",
				},
			},
		},
		FAQ: []faqItem{homeFAQ[0], homeFAQ[2], faqWhatsApp, homeFAQ[4], homeFAQ[1]},
	},
	{
		Path:        "/whatsapp-alternative",
		Title:       "חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל",
		H1:          "חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל",
		Description: "ערוץ עדכונים שנפתח בדפדפן, בלי אפליקציה ובלי מספר טלפון: מה דומה לערוץ וואטסאפ ולקבוצת מייל, מה שונה, ומתי כל אחד מהם מתאים יותר.",
		Lead:        "ערוץ וואטסאפ וקבוצה במייל הן שתי דרכים מוכרות להעביר עדכונים לקהל. ערוץ כאן עושה את אותה עבודה בדרך אחרת: דף אחד בדפדפן, שכל מי שיש לו את הקישור קורא בו.",
		Sections: []contentSection{
			{
				Heading: "מה דומה לערוץ וואטסאפ",
				Paragraphs: []string{
					"כמו בערוץ וואטסאפ, רק אתם ומי שצירפתם לצוות כותבים, והקוראים לא כותבים בערוץ. הודעה שפורסמה מופיעה אצל מי שהערוץ פתוח אצלו באותו רגע, ומי שמחובר יכול להגיב באימוג׳י.",
				},
			},
			{
				Heading: "מה שונה",
				List: []string{
					"בלי אפליקציה: הערוץ הוא אתר, ונפתח בכל דפדפן, בטלפון ובמחשב. כדי לקרוא ערוץ וואטסאפ צריך חשבון וואטסאפ.",
					"בלי מספר טלפון: בערוץ ציבורי הקוראים לא צריכים חשבון בכלל, רק את הקישור.",
					"גם במחשב, וגם למי שאין לו סמארטפון או שאינו משתמש באפליקציות מסרים.",
					"ההודעות נשארות בערוץ: קורא שמצטרף מאוחר רואה גם את מה שפורסם לפניו.",
				},
			},
			{
				Heading: "ובמקום קבוצת צ׳אט במייל",
				Paragraphs: []string{
					"בקבוצה במייל, תשובה של אחד החברים יכולה להגיע לתיבה של כולם, והעדכון החשוב נקבר בין התשובות. בערוץ רק הצוות כותב, וכל ההודעות נמצאות במקום אחד, לפי הסדר.",
					"ההבדל העיקרי: ערוץ לא שולח את ההודעות למייל. הקוראים נכנסים לקישור כשנוח להם, ומי שמתחבר לחשבון יכול לבקש התראה לנייד על כל פרסום, כשההתראות פעילות בערוץ.",
				},
			},
			{
				Heading: "מתי עדיף להישאר עם מה שיש",
				List: []string{
					"כשכל הקהל שלכם כבר בוואטסאפ ורוצה לקבל את העדכונים דווקא שם.",
					"כשחשוב שכל הודעה תגיע לתיבת המייל של כל אחד מהקוראים.",
					"כשאתם רוצים שיחה שבה כולם כותבים: ערוץ הוא כלי לפרסום עדכונים, לא לדיון.",
				},
			},
			{
				Heading: "למי ערוץ כאן מתאים",
				List: []string{
					"קהל מעורב: חלק בוואטסאפ, חלק רק במייל, וחלק בלי סמארטפון. כולם מקבלים אותו קישור.",
					"גולשים עם סינון אינטרנט: האתר פתוח בנטפרי, וכל ערוץ נפתח לגולשי נטפרי אחרי אישור נפרד מהם.",
					"קהילות, מוסדות ועסקים שרוצים קישור אחד קבוע לכל העדכונים, שאפשר לשים באתר, בעלון או בחתימת המייל.",
				},
			},
		},
		FAQ: []faqItem{faqWhatsApp, homeFAQ[2], homeFAQ[3], homeFAQ[1]},
	},
	{
		Path:        "/netfree-channel",
		Title:       "ערוץ עדכונים בנטפרי: איך פותחים ערוץ לגולשי נטפרי",
		H1:          "ערוץ עדכונים לגולשי נטפרי",
		Description: "האתר פתוח בנטפרי, אבל כל ערוץ צריך אישור נפרד מנטפרי לפני שגולשי נטפרי יכולים לקרוא אותו. כך מבקשים לפתוח ערוץ, ומה כדאי לכתוב בבקשה.",
		Lead:        "האתר עצמו פתוח בנטפרי. ערוץ חדש הוא דף חדש באתר, עם תוכן שבעלי הערוץ כותבים, ולכן גולשי נטפרי יכולים לקרוא אותו רק אחרי שנטפרי אישרו אותו בנפרד. כך מבקשים.",
		Sections: []contentSection{
			{
				Heading: "למה צריך אישור לכל ערוץ",
				Paragraphs: []string{
					"האישור של האתר הראשי לא חל אוטומטית על הערוצים שבו. כל ערוץ הוא דף נפרד, והתוכן שלו נכתב בידי מי שפתח אותו, ולכן נטפרי צריכים לאשר אותו בנפרד.",
					"עד שהערוץ נפתח, גולשי נטפרי לא יכולים לקרוא אותו. מי שגולש בלי סינון, או עם סינון אחר, אינו מושפע מזה.",
				},
			},
			{
				Heading: "איך מבקשים לפתוח ערוץ",
				Ordered: true,
				List: []string{
					"פותחים את הערוץ ומעתיקים את הכתובת המלאה שלו, כפי שהיא מופיעה בשורת הכתובת של הדפדפן.",
					"פונים לנטפרי בבקשה לפתוח את הכתובת הזו, בדרך שבה פונים אליהם לבקשת פתיחת אתר.",
					"כותבים בבקשה במה הערוץ עוסק ולמי הוא מיועד. אם יש בערוץ תמונות או קבצים, מבקשים במפורש שגם הם ייפתחו.",
					"אחרי האישור, גולשי נטפרי פותחים את אותו קישור וקוראים את הערוץ.",
				},
				ChannelURLExample: true,
			},
			{
				Heading: "כדאי לדעת",
				List: []string{
					"הבקשה היא לכל ערוץ בנפרד. מי שמנהל כמה ערוצים מבקש לכל אחד מהם.",
					"לא רק בעלי הערוץ יכולים לבקש: גם קורא שגולש בנטפרי יכול לפנות אליהם בבקשה לפתוח ערוץ שהוא רוצה לקרוא.",
					"כדאי לבקש את הפתיחה מיד כשהערוץ נפתח, לפני ששולחים את הקישור לקהל.",
				},
			},
		},
		FAQ: []faqItem{faqNetFree, homeFAQ[2], homeFAQ[1]},
	},
}

func findContentPage(p string) *contentPage {
	p = strings.TrimRight(p, "/")
	for i := range contentPages {
		if contentPages[i].Path == p {
			return &contentPages[i]
		}
	}
	return nil
}

// --- structured data -------------------------------------------------------

func faqPageLD(faq []faqItem, url string) map[string]any {
	items := make([]map[string]any, 0, len(faq))
	for _, f := range faq {
		items = append(items, map[string]any{
			"@type":          "Question",
			"name":           f.Q,
			"acceptedAnswer": map[string]any{"@type": "Answer", "text": f.A},
		})
	}
	return map[string]any{"@type": "FAQPage", "@id": url + "#faq", "inLanguage": "he", "mainEntity": items}
}

func breadcrumbLD(crumbs ...[2]string) map[string]any {
	items := make([]map[string]any, 0, len(crumbs))
	for i, c := range crumbs {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": c[0], "item": c[1]})
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
}

func websiteLD(origin, siteName string) map[string]any {
	return map[string]any{
		"@type":      "WebSite",
		"@id":        origin + "/#website",
		"name":       siteName,
		"url":        origin + "/",
		"inLanguage": "he",
		"publisher":  map[string]any{"@id": origin + "/#organization"},
	}
}

func homeStructuredData(origin, siteName string) []string {
	return []string{jsonLD(map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			map[string]any{
				"@type": "Organization",
				"@id":   origin + "/#organization",
				"name":  siteName,
				"url":   origin + "/",
				"logo":  origin + "/assets/icon-512.png",
			},
			websiteLD(origin, siteName),
			map[string]any{
				"@type":               "WebApplication",
				"name":                siteName,
				"url":                 origin + "/",
				"applicationCategory": "CommunicationApplication",
				"operatingSystem":     "כל דפדפן",
				"inLanguage":          "he",
				"description":         homeDescription,
			},
			faqPageLD(homeFAQ, origin+"/"),
		},
	})}
}

// channelStructuredData describes a channel page. pageName is the search
// title its managers wrote (or the name); the breadcrumb keeps the name.
func channelStructuredData(origin, siteName, name, pageName, description, canonical, image string) []string {
	url := origin + canonical
	return []string{jsonLD(map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			map[string]any{
				"@type":       "CollectionPage",
				"@id":         url,
				"name":        pageName,
				"description": description,
				"url":         url,
				"inLanguage":  "he",
				"image":       image,
				"isPartOf":    map[string]any{"@id": origin + "/#website"},
			},
			websiteLD(origin, siteName),
			breadcrumbLD([2]string{siteName, origin + "/"}, [2]string{name, url}),
		},
	})}
}

func contentPageStructuredData(origin, siteName string, p *contentPage) []string {
	url := origin + p.Path
	graph := []any{
		map[string]any{
			"@type":        "WebPage",
			"@id":          url,
			"name":         p.H1,
			"description":  p.Description,
			"url":          url,
			"inLanguage":   "he",
			"dateModified": contentRevised,
			"isPartOf":     map[string]any{"@id": origin + "/#website"},
		},
		websiteLD(origin, siteName),
		breadcrumbLD([2]string{siteName, origin + "/"}, [2]string{p.H1, url}),
	}
	if len(p.FAQ) > 0 {
		graph = append(graph, faqPageLD(p.FAQ, url))
	}
	return []string{jsonLD(map[string]any{"@context": "https://schema.org", "@graph": graph})}
}

// --- server-rendered content ----------------------------------------------

// staticShellStyle styles what the server puts inside <app-root> for the
// moment before the app starts. Scoped to .ssr so nothing leaks into the app,
// whose bootstrap replaces the element's content.
const staticShellStyle = `<style>.ssr{max-width:760px;margin:0 auto;padding:2rem 1.25rem;font-family:Assistant,system-ui,sans-serif;color:#1f2433;line-height:1.6}.ssr h1{font-size:1.9rem;font-weight:800;line-height:1.25;margin:0 0 .75rem}.ssr h2{font-size:1.25rem;margin:1.75rem 0 .5rem}.ssr h3{font-size:1rem;margin:1rem 0 .25rem}.ssr p{margin:0 0 .75rem}.ssr a{color:#4347d9}.ssr ul{padding-inline-start:1.25rem}</style>`

func faqHTML(faq []faqItem) string {
	var b strings.Builder
	for _, f := range faq {
		b.WriteString("<h3>" + html.EscapeString(f.Q) + "</h3><p>" + html.EscapeString(f.A) + "</p>")
	}
	return b.String()
}

func contentLinksHTML() string {
	var b strings.Builder
	b.WriteString("<ul>")
	for _, p := range contentPages {
		b.WriteString(`<li><a href="` + p.Path + `">` + html.EscapeString(p.H1) + `</a></li>`)
	}
	b.WriteString("</ul>")
	return b.String()
}

// homeStaticBody is the home page as a crawler that runs no script sees it,
// and what a visitor sees for the instant before the landing page renders.
func homeStaticBody(siteName string) string {
	var b strings.Builder
	b.WriteString(staticShellStyle)
	b.WriteString(`<main class="ssr">`)
	b.WriteString("<h1>" + html.EscapeString(homeH1) + "</h1>")
	b.WriteString("<p>" + html.EscapeString(homeLead) + "</p>")
	b.WriteString(`<p><a href="/login">פתיחת ערוץ בחינם</a></p>`)
	b.WriteString("<h2>שאלות נפוצות</h2>")
	b.WriteString(faqHTML(homeFAQ))
	b.WriteString("<h2>מדריכים</h2>")
	b.WriteString(contentLinksHTML())
	b.WriteString("<p>" + html.EscapeString(siteName) + "</p>")
	b.WriteString("</main>")
	return b.String()
}

// homeH1 and homeLead match the landing page's hero
// (landing-page.component.ts), so the static and the rendered page say the
// same thing.
const (
	homeH1   = "ערוץ עדכונים משלכם, פתוח תוך רגע"
	homeLead = "ערוץ הוא דף אחד שבו אתם מפרסמים הודעות, תמונות, סרטונים וקישורים, והם מגיעים לכל מי שיש לו את הקישור. חלופה לערוץ וואטסאפ ולקבוצות צ׳אט במייל: הקוראים לא צריכים להתקין כלום ולא להירשם."
)

// channelStaticBody names the channel in the initial HTML. The feed itself
// is loaded by the app.
func channelStaticBody(siteName, name, description string) string {
	var b strings.Builder
	b.WriteString(staticShellStyle)
	b.WriteString(`<main class="ssr">`)
	b.WriteString("<h1>" + html.EscapeString(name) + "</h1>")
	if d := strings.TrimSpace(description); d != "" {
		b.WriteString("<p>" + html.EscapeString(d) + "</p>")
	}
	b.WriteString(`<p>ערוץ עדכונים ב<a href="/">` + html.EscapeString(siteName) + `</a>. ההודעות נטענות…</p>`)
	// Every public channel is a way in to the site: the link tells a crawler
	// where channels come from, and is replaced by the app's own header (whose
	// credit links to "/") once it starts.
	b.WriteString(`<p><a href="/">פתיחת ערוץ עדכונים משלכם, בחינם</a> · <a href="/open-channel">מדריך לפתיחת ערוץ</a></p>`)
	b.WriteString("</main>")
	return b.String()
}

var contentPageTmpl = template.Must(template.New("content").Parse(`<!doctype html>
<html lang="he" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Page.Description}}">
<meta name="theme-color" content="#5a5ff5">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Assistant:wght@400;700;800&display=swap" rel="stylesheet">
<link rel="icon" type="image/x-icon" href="/assets/favicon.ico">
<link rel="icon" type="image/svg+xml" href="/assets/icon.svg">
<link rel="apple-touch-icon" href="/assets/apple-touch-icon.png">
<link rel="manifest" href="/assets/manifest.webmanifest">
<style>
:root{--ink:#1f2433;--muted:#5b6275;--brand:#5a5ff5;--brand-ink:#3e42c9;--bg:#f6f7fb;--card:#fff;--line:#e3e6ef}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font-family:Assistant,system-ui,-apple-system,"Segoe UI",sans-serif;font-size:1.0625rem;line-height:1.7}
a{color:var(--brand-ink)}
.top{display:flex;align-items:center;justify-content:space-between;gap:1rem;padding:.75rem 1.25rem;background:var(--card);border-bottom:1px solid var(--line)}
.brand{font-weight:800;font-size:1.2rem;color:var(--ink);text-decoration:none}
.wrap{max-width:760px;margin:0 auto;padding:0 1.25rem}
.crumbs{font-size:.9rem;color:var(--muted);margin:1.25rem 0 0}
.crumbs a{color:var(--muted)}
h1{font-size:clamp(1.75rem,5vw,2.4rem);font-weight:800;line-height:1.2;margin:.5rem 0 .75rem}
.lead{font-size:1.15rem;color:var(--muted);margin:0 0 1.5rem}
h2{font-size:1.35rem;font-weight:800;margin:2rem 0 .5rem}
h3{font-size:1.05rem;font-weight:700;margin:1.25rem 0 .25rem}
p{margin:0 0 .85rem}
ul,ol{padding-inline-start:1.4rem;margin:0 0 1rem}
li{margin-bottom:.4rem}
.cta{display:inline-block;margin:.5rem 0 1rem;padding:.8rem 1.6rem;border-radius:.75rem;background:var(--brand);color:#fff;font-weight:700;text-decoration:none}
.cta:focus-visible,.brand:focus-visible,a:focus-visible{outline:3px solid var(--brand);outline-offset:2px}
.url{font-family:ui-monospace,Menlo,Consolas,monospace;font-size:.95rem;background:var(--card);border:1px solid var(--line);border-radius:.5rem;padding:.5rem .75rem;overflow-wrap:anywhere;text-align:left}
.card{background:var(--card);border:1px solid var(--line);border-radius:1rem;padding:1.25rem 1.5rem;margin:1.5rem 0}
footer{margin-top:2.5rem;padding:1.5rem 1.25rem;text-align:center;font-size:.9rem;color:var(--muted);border-top:1px solid var(--line);background:var(--card)}
footer a{margin:0 .5rem}
</style>
{{.Head}}</head>
<body>
<header class="top"><a class="brand" href="/">{{.SiteName}}</a><a href="/login">התחברות</a></header>
<main class="wrap">
<nav class="crumbs" aria-label="מיקום באתר"><a href="/">{{.SiteName}}</a> › {{.Page.H1}}</nav>
<h1>{{.Page.H1}}</h1>
<p class="lead">{{.Page.Lead}}</p>
<a class="cta" href="/login">פתיחת ערוץ בחינם</a>
{{range .Page.Sections}}<section>
<h2>{{.Heading}}</h2>
{{range .Paragraphs}}<p>{{.}}</p>
{{end}}{{if .List}}{{if .Ordered}}<ol>{{else}}<ul>{{end}}{{range .List}}<li>{{.}}</li>{{end}}{{if .Ordered}}</ol>{{else}}</ul>{{end}}{{end}}
{{if .ChannelURLExample}}<p>כתובת של ערוץ נראית כך, ובמקום <span dir="ltr">your-channel</span> מופיעה הכתובת של הערוץ שלכם:</p>
<p class="url" dir="ltr">{{$.Origin}}/channel/your-channel</p>
{{end}}</section>
{{end}}{{if .Page.FAQ}}<section class="card">
<h2>שאלות נפוצות</h2>
{{range .Page.FAQ}}<h3>{{.Q}}</h3>
<p>{{.A}}</p>
{{end}}</section>
{{end}}<p><a class="cta" href="/login">פתיחת ערוץ בחינם</a></p>
</main>
<footer><a href="/">{{.SiteName}}: פתיחת ערוץ עדכונים בחינם</a>{{range .Others}}<a href="{{.Path}}">{{.H1}}</a>{{end}}<a href="/#contact">יצירת קשר</a></footer>
</body>
</html>
`))

// serveContentPage answers one of the server-rendered pages. Nothing on them
// needs the app, so they are complete documents of their own rather than the
// SPA shell: the Angular router does not know these paths and would send the
// visitor to the home page.
func serveContentPage(w http.ResponseWriter, r *http.Request, p *contentPage, cfg *SettingConfig) {
	if canon := p.Path; r.URL.Path != canon {
		// One URL per page: "/open-channel/" would otherwise be a duplicate.
		http.Redirect(w, r, canon, http.StatusMovedPermanently)
		return
	}
	origin := requestOrigin(r, cfg)
	siteName := siteNameOf(cfg)
	title := p.Title + " · " + siteName

	m := pageMeta{
		Status:         http.StatusOK,
		Title:          title,
		Description:    p.Description,
		Canonical:      p.Path,
		Image:          origin + "/assets/og.jpg",
		ImageAlt:       siteName,
		Card:           "summary_large_image",
		StructuredData: contentPageStructuredData(origin, siteName, p),
	}
	// The head tags are produced by the same code as every other page, by
	// injecting into an empty head and taking what was added.
	head := injectHeadTags([]byte("<title></title></head>"), origin, siteName, p.Path, m, cfg)
	head = bytes.TrimPrefix(head, []byte("<title>"+html.EscapeString(title)+"</title>"))
	head = bytes.TrimSuffix(head, []byte("</head>"))

	others := make([]contentPage, 0, len(contentPages))
	for _, o := range contentPages {
		if o.Path != p.Path {
			others = append(others, o)
		}
	}

	var buf bytes.Buffer
	if err := contentPageTmpl.Execute(&buf, struct {
		Title    string
		SiteName string
		Origin   string
		Page     *contentPage
		Others   []contentPage
		Head     template.HTML
	}{title, siteName, origin, p, others, template.HTML(head)}); err != nil {
		log.Printf("content page %s: %v\n", p.Path, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	out := buf.Bytes()
	if cfg != nil && cfg.AnalyticsHead != "" {
		out = bytes.Replace(out, []byte("</head>"), []byte(cfg.AnalyticsHead+"</head>"), 1)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(out)
}
