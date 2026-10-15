package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// Inbound support mail.
//
// Mail to the channels mailbox (channels@magnetads.co.il) lands in Magnet:
// its email-events-webhook files it in Magnet's CRM and then hands it here,
// over the same shared secret magnet_mail.go uses the other way:
//
//	POST /api/integrations/magnet/inbound-mail
//	x-channels-secret: $MAGNET_MAIL_SECRET
//	{"from_email", "from_name", "subject", "text", "message_id",
//	 "in_reply_to", "references", "ticket_ref", "sender_verified"}
//
// Every such mail becomes part of the support inbox: a reply to one of the
// sender's threads is appended to it, anything else opens a new ticket.
//
// Who may add to an existing thread
// ---------------------------------
// The From address of a mail is whatever the sender typed into their client.
// Appending on From alone would let anyone plant a message in someone else's
// thread — and the operator would answer it, to the real owner, in context.
// So a mail joins a thread only when Magnet reports the sender as verified
// (sender_verified: DMARC pass, or a DKIM signature aligned with the From
// domain, as the receiving server judged it). An unverified sender gets a new
// ticket of their own, which is exactly what the web form gives an anonymous
// visitor today. Even then the ticket ref (below) is not a credential: it
// only picks among the threads whose email already equals the sender's.
//
// Which thread
// ------------
//  1. ticket_ref — the short id Magnet's support_reply mail carries in its
//     subject ("[פנייה #1a2b3c4d]"). A prefix of the ticket id, matched only
//     among the sender's own tickets. If it names a ticket that is closed or
//     full, the mail opens a new ticket rather than landing in some other
//     thread of theirs.
//  2. Otherwise the sender's most recently active ticket that is not closed.
//  3. Otherwise a new ticket.
//
// The sender's tickets are found through supportMatchIndexKey, an index of
// every ticket by email (anonymous ones included) that only this file reads.
// The user index behind "my tickets" is untouched: a ticket opened by mail is
// not authenticated and never appears there.
//
// A mail-opened ticket has no access token. The requester continues the
// conversation by mail — the operator's answer reaches them as support_reply,
// because the ticket has an email — and nothing that could open the thread on
// the site is ever sent anywhere.

const (
	supportInboundSource = "email"
	// defaultInboundSubject names a mail that arrived without one.
	defaultInboundSubject = "פנייה במייל"
	emptyInboundBody      = "(ללא תוכן)"

	// supportInboundSeenPrefix records each message_id already taken in, for
	// as long as a ticket lives: Magnet retries a failed hand-off, and the
	// provider redelivers webhooks.
	supportInboundSeenPrefix = "support_inbound:seen:"
	// How many of a sender's tickets the match looks through, newest first.
	maxInboundMatchScan = 50
)

// inboundTicketRefRe is the shape of a ticket ref: hex, as ticket ids are.
var inboundTicketRefRe = regexp.MustCompile(`^[0-9a-f]{6,32}$`)

// inboundSubjectNoise is what a reply adds around the subject Magnet sent:
// client prefixes, the support_reply wording, and the ticket tag.
var (
	inboundReplyPrefixRe = regexp.MustCompile(`^\s*(?i:re|fwd?|aw|sv|tr|תגובה|השב|העברה)\s*:\s*`)
	inboundTicketTagRe   = regexp.MustCompile(`\s*\[\s*פנייה\s*#?\s*[0-9A-Fa-f]{4,32}\s*\]\s*`)
)

const supportReplyMailSubjectPrefix = "התקבלה תשובה לפנייה שלך:"

// cleanInboundSubject turns "Re: התקבלה תשובה לפנייה שלך: X [פנייה #1a2b]"
// back into "X", for a mail that opens a new ticket.
func cleanInboundSubject(s string) string {
	s = oneLine(s)
	s = inboundTicketTagRe.ReplaceAllString(s, " ")
	for {
		next := inboundReplyPrefixRe.ReplaceAllString(s, "")
		next = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(next), supportReplyMailSubjectPrefix))
		if next == s {
			break
		}
		s = next
	}
	return trimTo(s, maxSupportSubjectLen)
}

// inboundMailLimiters throttle per sender. A person writes a handful of mails
// an hour at most; this exists for the auto-responder that answers every
// mail, and for a leaked secret. 10 at once, then one per 3 minutes.
var (
	inboundMailLimiters sync.Map
	inboundMailMu       sync.Mutex
)

func inboundMailLimiter(email string) *rate.Limiter {
	return getLimiter(&inboundMailLimiters, &inboundMailMu, "email:"+email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(3*time.Minute), 10)
	})
}

type inboundMailRequest struct {
	FromEmail      string `json:"from_email"`
	FromName       string `json:"from_name"`
	Subject        string `json:"subject"`
	Text           string `json:"text"`
	MessageID      string `json:"message_id"`
	InReplyTo      string `json:"in_reply_to"`
	References     string `json:"references"`
	TicketRef      string `json:"ticket_ref"`
	SenderVerified bool   `json:"sender_verified"`
}

type inboundMailResult struct {
	OK        bool   `json:"ok"`
	TicketID  string `json:"ticket_id"`
	Action    string `json:"action"` // "appended" | "created"
	Duplicate bool   `json:"duplicate,omitempty"`
}

func writeInboundJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func inboundError(w http.ResponseWriter, status int, msg string) {
	writeInboundJSON(w, status, map[string]string{"error": msg})
}

// inboundSecretOK checks x-channels-secret in constant time. The caller has
// already made sure a secret is configured.
func inboundSecretOK(r *http.Request) bool {
	got := r.Header.Get("x-channels-secret")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(magnetMailSecret)) == 1
}

// POST /api/integrations/magnet/inbound-mail — server to server, from Magnet.
func magnetInboundMail(w http.ResponseWriter, r *http.Request) {
	// Fail closed: with no secret configured this route is not a door at all.
	if magnetMailSecret == "" {
		inboundError(w, http.StatusServiceUnavailable, "הקליטה של מיילים נכנסים אינה מוגדרת")
		return
	}
	if !inboundSecretOK(r) {
		inboundError(w, http.StatusUnauthorized, "אין הרשאה")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var req inboundMailRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		inboundError(w, http.StatusBadRequest, "בקשה לא תקינה")
		return
	}
	from := normEmail(req.FromEmail)
	if len(from) > maxSupportEmailLen || !looksLikeEmail(from) {
		inboundError(w, http.StatusBadRequest, "כתובת השולח אינה תקינה")
		return
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" || len(messageID) > 1000 {
		inboundError(w, http.StatusBadRequest, "חסר מזהה הודעה")
		return
	}

	seenKey := inboundSeenKey(messageID)
	if prev, err := rdb.Get(ctx, seenKey).Result(); err == nil && prev != "" && prev != "pending" {
		writeInboundJSON(w, http.StatusOK, inboundMailResult{OK: true, TicketID: prev, Action: "duplicate", Duplicate: true})
		return
	}

	if !allowOrRetryAfterJSON(w, inboundMailLimiter(from)) {
		return
	}

	// Claim the message id before writing anything, so two concurrent
	// deliveries of the same mail cannot both add it. A claim whose request
	// died expires quickly and the next retry takes it.
	claimed, err := rdb.SetNX(ctx, seenKey, "pending", time.Minute).Result()
	if err != nil {
		inboundError(w, http.StatusInternalServerError, "שגיאה בשמירה")
		return
	}
	if !claimed {
		prev, _ := rdb.Get(ctx, seenKey).Result()
		if prev != "" && prev != "pending" {
			writeInboundJSON(w, http.StatusOK, inboundMailResult{OK: true, TicketID: prev, Action: "duplicate", Duplicate: true})
			return
		}
		inboundError(w, http.StatusConflict, "ההודעה נקלטת כרגע")
		return
	}

	res, err := ingestInboundMail(ctx, &req, from)
	if err != nil {
		rdb.Del(context.WithoutCancel(ctx), seenKey)
		log.Printf("inbound mail: not stored: %v", err)
		inboundError(w, http.StatusInternalServerError, "שגיאה בשמירה")
		return
	}
	rdb.Set(context.WithoutCancel(ctx), seenKey, res.TicketID, supportTicketTTL)
	writeInboundJSON(w, http.StatusOK, res)
}

func inboundSeenKey(messageID string) string {
	sum := sha256.Sum256([]byte(messageID))
	return supportInboundSeenPrefix + hex.EncodeToString(sum[:])
}

// allowOrRetryAfterJSON is allowOrRetryAfter with a JSON body, which is what
// Magnet logs.
func allowOrRetryAfterJSON(w http.ResponseWriter, l *rate.Limiter) bool {
	res := l.Reserve()
	if !res.OK() {
		inboundError(w, http.StatusTooManyRequests, "יותר מדי הודעות מהשולח הזה, נסו שוב מאוחר יותר")
		return false
	}
	if d := res.Delay(); d > 0 {
		res.Cancel()
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
		inboundError(w, http.StatusTooManyRequests, "יותר מדי הודעות מהשולח הזה, נסו שוב מאוחר יותר")
		return false
	}
	return true
}

// ingestInboundMail appends the mail to the sender's thread or opens a new
// one. from is already normalised.
func ingestInboundMail(ctx context.Context, req *inboundMailRequest, from string) (*inboundMailResult, error) {
	body := trimTo(req.Text, maxSupportBodyLen)
	if body == "" {
		body = emptyInboundBody
	}

	if req.SenderVerified {
		target, err := findInboundTicket(ctx, from, strings.ToLower(strings.TrimSpace(req.TicketRef)))
		if err != nil {
			return nil, err
		}
		if target != "" {
			updated, err := dbUpdateSupportTicket(ctx, target, func(cur *SupportTicket) error {
				// Judged again on the ticket as it stands at write time.
				if !strings.EqualFold(cur.Email, from) {
					return errInboundNoMatch
				}
				if cur.Status == SupportStatusClosed {
					return errTicketClosed
				}
				if len(cur.Messages) >= maxSupportMessages {
					return errThreadFull
				}
				appendSupportMessage(cur, "user", cur.Name, body, SupportStatusOpen)
				cur.Messages[len(cur.Messages)-1].Source = supportInboundSource
				return nil
			})
			switch {
			case err == nil:
				return &inboundMailResult{OK: true, TicketID: updated.ID, Action: "appended"}, nil
			case errors.Is(err, errTicketClosed), errors.Is(err, errThreadFull),
				errors.Is(err, errInboundNoMatch), errors.Is(err, redis.Nil):
				// Closed, full or gone between the lookup and the write: the
				// mail still has to land somewhere, so it opens a new ticket.
			default:
				return nil, err
			}
		}
	}

	id := generatedRandomID(16)
	if id == "" {
		return nil, errors.New("random id")
	}
	name := oneLine(trimTo(req.FromName, maxSupportNameLen))
	if name == "" {
		name = from
	}
	subject := cleanInboundSubject(req.Subject)
	if subject == "" {
		subject = defaultInboundSubject
	}
	now := time.Now()
	t := &SupportTicket{
		ID:      id,
		Subject: subject,
		Name:    name,
		Email:   from,
		// The From of a mail proves no more than an address typed into the
		// form, so the ticket is never listed under that user (see
		// dbSaveSupportTicket) — verified or not.
		Authenticated: false,
		Status:        SupportStatusOpen,
		Source:        supportInboundSource,
		Messages: []SupportMessage{{
			Author: "user", AuthorName: name, Body: body, CreatedAt: now, Source: supportInboundSource,
		}},
		CreatedAt: now,
		UpdatedAt: now,
		// No AccessToken: the requester continues by mail, and a token that
		// is never handed out is one that cannot leak.
	}
	if err := dbSaveSupportTicket(ctx, t); err != nil {
		return nil, err
	}
	return &inboundMailResult{OK: true, TicketID: id, Action: "created"}, nil
}

var errInboundNoMatch = errors.New("ticket email does not match the sender")

// findInboundTicket picks the thread a verified sender's mail belongs to, or
// "" for none. ref, when it names one of the sender's tickets, decides alone:
// a closed or full ticket then yields "" (a new ticket) rather than some other
// thread of theirs.
func findInboundTicket(ctx context.Context, from, ref string) (string, error) {
	key := supportMatchIndexKey(from)
	ids, err := rdb.ZRevRange(ctx, key, 0, maxInboundMatchScan-1).Result()
	if err != nil {
		return "", err
	}
	if !inboundTicketRefRe.MatchString(ref) {
		ref = ""
	}

	var latestOpen string
	for _, id := range ids {
		t, err := dbGetSupportTicket(ctx, id)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Expired body: drop the stale index entry.
				rdb.ZRem(ctx, key, id)
				continue
			}
			return "", err
		}
		if !strings.EqualFold(t.Email, from) {
			continue
		}
		usable := t.Status != SupportStatusClosed && len(t.Messages) < maxSupportMessages
		if ref != "" && strings.HasPrefix(strings.ToLower(t.ID), ref) {
			if usable {
				return t.ID, nil
			}
			return "", nil
		}
		if usable && latestOpen == "" {
			latestOpen = t.ID
		}
	}
	return latestOpen, nil
}

// backfillSupportMatchIndex puts the tickets written before the match index
// existed into it, so a mail answering one of them still finds its thread.
func backfillSupportMatchIndex(ctx context.Context) error {
	ids, err := rdb.ZRange(ctx, supportTicketIndexKey, 0, -1).Result()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := budgetExhausted(ctx); err != nil {
			return err
		}
		t, err := dbGetSupportTicket(ctx, id)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return err
		}
		if t.Email == "" {
			continue
		}
		key := supportMatchIndexKey(t.Email)
		if err := rdb.ZAdd(ctx, key, redis.Z{Score: float64(t.UpdatedAt.Unix()), Member: t.ID}).Err(); err != nil {
			return err
		}
		rdb.Expire(ctx, key, supportTicketTTL)
	}
	return nil
}
