package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	codeTTL         = 10 * time.Minute
	maxCodeAttempts = 5
	sessionTTL      = 24 * time.Hour
	sessionCookie   = "__Host-tolon_session"
	codesPerEmail   = 3 // per hour, persistent
	codesPerEmailDy = 5 // per 24 h, persistent
	burnsToLockout  = 3 // burnt codes within 24 h that lock the email out
	lockoutFor      = 24 * time.Hour
)

var emailRe = regexp.MustCompile(`^[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]{1,64}@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

// normaliseEmail trims and lowercases; returns "" when the address is not acceptable.
func normaliseEmail(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if len(e) > 254 || !emailRe.MatchString(e) || strings.Contains(e, "..") {
		return ""
	}
	return e
}

func (s *server) mac(parts ...string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(m.Sum(nil))
}

// logID is the only form of an email that reaches the logs.
func (s *server) logID(email string) string { return s.mac("log", email)[:12] }

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// --- Turnstile ---------------------------------------------------------------

type turnstile struct {
	secret    string
	verifyURL string
	hostname  string
	client    *http.Client
}

func (t *turnstile) verify(token string) bool {
	if t == nil || token == "" || len(token) > 2048 {
		return false
	}
	resp, err := t.client.PostForm(t.verifyURL, url.Values{"secret": {t.secret}, "response": {token}})
	if err != nil {
		log.Printf("turnstile: %v", err)
		return false
	}
	defer resp.Body.Close()
	var r struct {
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&r) != nil {
		return false
	}
	return r.Success && r.Hostname == t.hostname
}

// --- Mail --------------------------------------------------------------------

type mailer interface {
	send(to, subject, body string) error
}

type smtpMailer struct {
	host, port, user, pass, from string
}

// send delivers over implicit TLS (465) or mandatory STARTTLS (any other port); plaintext is refused.
func (m *smtpMailer) send(to, subject, body string) error {
	addr := net.JoinHostPort(m.host, m.port)
	tlsCfg := &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if m.port == "465" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsCfg)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if m.port != "465" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errNoSTARTTLS
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if m.user != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(m.from) // SMTP_FROM may carry a display name
	if err != nil {
		return err
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMessage(from, to, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

var errNoSTARTTLS = errors.New("smtp: server does not offer STARTTLS")

// smtpErrClass describes a send failure without its text, which can echo the recipient address.
func smtpErrClass(err error) string {
	var tp *textproto.Error
	var ne net.Error
	var rec tls.RecordHeaderError
	var cert *tls.CertificateVerificationError
	switch {
	case errors.Is(err, errNoSTARTTLS):
		return "no STARTTLS offered"
	case errors.As(err, &tp):
		return fmt.Sprintf("smtp reply %d (class %dxx)", tp.Code, tp.Code/100)
	case errors.As(err, &cert), errors.As(err, &rec):
		return "tls error"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.As(err, &ne):
		return "network error"
	default:
		return fmt.Sprintf("error %T", err)
	}
}

// --- Handlers ----------------------------------------------------------------

func (s *server) handleOTPRequest(w http.ResponseWriter, r *http.Request) {
	if s.closed() {
		writeJSON(w, http.StatusGone, errBody("poll_closed"))
		return
	}
	if s.mail == nil {
		writeJSON(w, http.StatusServiceUnavailable, errBody("email_unavailable"))
		return
	}
	var req struct {
		Email     string `json:"email"`
		Turnstile string `json:"turnstile"`
		Locale    string `json:"locale"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email := normaliseEmail(req.Email)
	if email == "" {
		writeJSON(w, http.StatusBadRequest, errBody("invalid"))
		return
	}
	if !s.otpIPLimit.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, errBody("rate_limited"))
		return
	}
	if !s.captcha.verify(req.Turnstile) {
		writeJSON(w, http.StatusBadRequest, errBody("turnstile_failed"))
		return
	}
	n, err := randInt(1_000_000)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	code := fmt.Sprintf("%06d", n)
	switch err := s.storeCode(email, code); {
	case errors.Is(err, errRateLimited):
		writeJSON(w, http.StatusTooManyRequests, errBody("rate_limited"))
		return
	case err != nil:
		log.Printf("otp store %s: %v", s.logID(email), err)
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	// Sent whether or not the address already voted, so the response reveals nothing.
	go func() {
		subject, body := s.codeEmail(req.Locale, code)
		if err := s.mail.send(email, subject, body); err != nil {
			log.Printf("otp mail %s: %s", s.logID(email), smtpErrClass(err))
			return
		}
		log.Printf("otp sent %s", s.logID(email))
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// emailLocales are the languages the code email is sent in.
var emailLocales = []string{"pt-BR", "de", "en-US", "es", "ar", "tr", "hi", "zh-CN", "ja"}

// normaliseLocale maps a requested locale onto emailLocales; unknown values become en-US.
func normaliseLocale(l string) string {
	l = strings.TrimSpace(l)
	for _, a := range emailLocales {
		if strings.EqualFold(l, a) {
			return a
		}
	}
	lang := strings.ToLower(l)
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	switch lang {
	case "pt":
		return "pt-BR"
	case "en":
		return "en-US"
	case "zh":
		return "zh-CN"
	case "es":
		return "es"
	}
	return "en-US"
}

type emailText struct{ subject, body string }

var defaultEmail = emailText{"Your Tolon poll code",
	"Your code is {code}. It expires in {minutes} minutes. If you did not ask for it, ignore this email."}

// loadEmailTexts reads email.subject and email.body from <webDir>/i18n/<locale>.json.
// A missing file or key falls back to en-US, and a missing en-US to defaultEmail.
func loadEmailTexts(webDir string) map[string]emailText {
	read := func(locale string) (t emailText) {
		b, err := os.ReadFile(filepath.Join(webDir, "i18n", locale+".json"))
		if err != nil {
			return t
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(b, &doc) != nil {
			log.Printf("i18n %s: invalid JSON", locale)
			return t
		}
		var nested struct{ Subject, Body string }
		_ = json.Unmarshal(doc["email"], &nested)
		var flat string
		if t.subject = nested.Subject; t.subject == "" && json.Unmarshal(doc["email.subject"], &flat) == nil {
			t.subject = flat
		}
		flat = ""
		if t.body = nested.Body; t.body == "" && json.Unmarshal(doc["email.body"], &flat) == nil {
			t.body = flat
		}
		return t
	}
	base := read("en-US")
	if base.subject == "" {
		base.subject = defaultEmail.subject
	}
	if base.body == "" {
		base.body = defaultEmail.body
	}
	texts := map[string]emailText{}
	for _, l := range emailLocales {
		t := read(l)
		if t.subject == "" {
			t.subject = base.subject
		}
		if t.body == "" {
			t.body = base.body
		}
		texts[l] = t
	}
	return texts
}

// codeEmail returns the subject and body for the requested locale with the code filled in.
func (s *server) codeEmail(locale, code string) (string, string) {
	t, ok := s.emails[normaliseLocale(locale)]
	if !ok {
		t = defaultEmail
	}
	r := strings.NewReplacer("{code}", code, "{minutes}", strconv.Itoa(int(codeTTL.Minutes())))
	return r.Replace(t.subject), r.Replace(t.body) + "\n"
}

// buildMessage renders a UTF-8 plain-text mail: RFC 2047 subject, quoted-printable body.
func buildMessage(from *mail.Address, to, subject, body string) []byte {
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	_, _ = w.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = w.Close()
	return []byte("From: " + from.String() + "\r\nTo: " + to +
		"\r\nSubject: " + mime.QEncoding.Encode("UTF-8", subject) +
		"\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8" +
		"\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + qp.String())
}

var errRateLimited = errors.New("rate limited")

// storeCode enforces the persistent send limits and replaces any previous code for the email.
func (s *server) storeCode(email, code string) error {
	key := s.mac("email", email)
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var perEmail, perEmailDay, perDay, locked int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM otp_lockouts WHERE email_key = ? AND until > ?`, key, now.Unix()).Scan(&locked); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM otp_sends WHERE email_key = ? AND sent_at > ?`, key, now.Add(-time.Hour).Unix()).Scan(&perEmail); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM otp_sends WHERE email_key = ? AND sent_at > ?`, key, now.Add(-24*time.Hour).Unix()).Scan(&perEmailDay); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM otp_sends WHERE sent_at > ?`, now.Add(-24*time.Hour).Unix()).Scan(&perDay); err != nil {
		return err
	}
	if locked > 0 || perEmail >= codesPerEmail || perEmailDay >= codesPerEmailDy || perDay >= s.codesPerDay {
		return errRateLimited
	}
	if _, err := tx.Exec(`INSERT INTO otp_sends (email_key, sent_at) VALUES (?, ?)`, key, now.Unix()); err != nil {
		return err
	}
	if warn := (s.codesPerDay*4 + 4) / 5; perDay+1 == warn { // log once when crossing 80 %
		log.Printf("warning: %d of %d daily code emails used", perDay+1, s.codesPerDay)
	}
	if _, err := tx.Exec(`INSERT INTO otp_codes (email_key, code_mac, expires_at, attempts) VALUES (?, ?, ?, 0)
		ON CONFLICT(email_key) DO UPDATE SET code_mac = excluded.code_mac, expires_at = excluded.expires_at, attempts = 0`,
		key, s.mac("code", email, code), now.Add(codeTTL).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) handleOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	email := normaliseEmail(req.Email)
	if email == "" || !s.checkCode(email, strings.TrimSpace(req.Code)) {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_code"))
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	id := hex.EncodeToString(raw)
	exp := s.now().Add(sessionTTL)
	if _, err := s.db.Exec(`INSERT INTO sessions (id_hash, email, expires_at) VALUES (?, ?, ?)`, sha(id), email, exp.Unix()); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", Expires: exp, MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	log.Printf("session created %s", s.logID(email))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// checkCode counts every attempt; a code is burnt after maxCodeAttempts and deleted once used.
func (s *server) checkCode(email, code string) bool {
	key := s.mac("email", email)
	tx, err := s.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()
	var stored string
	var exp int64
	var attempts int
	if tx.QueryRow(`SELECT code_mac, expires_at, attempts FROM otp_codes WHERE email_key = ?`, key).Scan(&stored, &exp, &attempts) != nil {
		return false
	}
	now := s.now()
	var locked int
	if tx.QueryRow(`SELECT COUNT(*) FROM otp_lockouts WHERE email_key = ? AND until > ?`, key, now.Unix()).Scan(&locked) != nil || locked > 0 {
		return false
	}
	if attempts >= maxCodeAttempts || now.Unix() > exp {
		return false
	}
	ok := subtle.ConstantTimeCompare([]byte(stored), []byte(s.mac("code", email, code))) == 1
	if ok {
		_, err = tx.Exec(`DELETE FROM otp_codes WHERE email_key = ?`, key)
	} else {
		_, err = tx.Exec(`UPDATE otp_codes SET attempts = attempts + 1 WHERE email_key = ?`, key)
		if err == nil && attempts+1 == maxCodeAttempts {
			err = s.recordBurn(tx, key, now)
		}
	}
	if err != nil || tx.Commit() != nil {
		return false
	}
	return ok
}

// recordBurn notes a burnt code and locks the email out once burnsToLockout codes burn within 24 h.
func (s *server) recordBurn(tx *sql.Tx, key string, now time.Time) error {
	if _, err := tx.Exec(`INSERT INTO otp_burns (email_key, burnt_at) VALUES (?, ?)`, key, now.Unix()); err != nil {
		return err
	}
	var burns int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM otp_burns WHERE email_key = ? AND burnt_at > ?`, key, now.Add(-24*time.Hour).Unix()).Scan(&burns); err != nil {
		return err
	}
	if burns < burnsToLockout {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO otp_lockouts (email_key, until) VALUES (?, ?)
		ON CONFLICT(email_key) DO UPDATE SET until = excluded.until`, key, now.Add(lockoutFor).Unix())
	return err
}

// sessionEmail returns the email of a valid session cookie, or "".
func (s *server) sessionEmail(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) != 64 {
		return ""
	}
	var email string
	if s.db.QueryRow(`SELECT email FROM sessions WHERE id_hash = ? AND expires_at > ?`, sha(c.Value), s.now().Unix()).Scan(&email) != nil {
		return ""
	}
	return email
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE id_hash = ?`, sha(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// closed reports whether the retention deadline (POLL_DELETE_AFTER) has passed.
func (s *server) closed() bool { return !s.closeAt.IsZero() && s.now().After(s.closeAt) }

func (s *server) prune() {
	for _, l := range []*limiter{s.hostLimit, s.apiLimit, s.otpIPLimit, s.voteLimit} {
		l.evict()
	}
	if s.closed() {
		for _, t := range []string{"votes", "sessions", "otp_codes", "otp_sends", "otp_burns", "otp_lockouts"} {
			if _, err := s.db.Exec(`DELETE FROM ` + t); err != nil {
				log.Printf("retention delete %s: %v", t, err)
			}
		}
		return
	}
	now := s.now().Unix()
	for _, q := range []string{
		`DELETE FROM sessions WHERE expires_at <= ?`,
		`DELETE FROM otp_codes WHERE expires_at <= ?`,
	} {
		_, _ = s.db.Exec(q, now)
	}
	day := now - int64((24 * time.Hour).Seconds())
	_, _ = s.db.Exec(`DELETE FROM otp_sends WHERE sent_at <= ?`, day)
	_, _ = s.db.Exec(`DELETE FROM otp_burns WHERE burnt_at <= ?`, day)
	_, _ = s.db.Exec(`DELETE FROM otp_lockouts WHERE until <= ?`, now)
}

func randInt(max int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}
