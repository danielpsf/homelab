package main

import (
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
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"regexp"
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
			return errors.New("smtp: server does not offer STARTTLS")
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
	msg := "From: " + from.String() + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
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
		subject, body := codeEmail(req.Locale, code)
		if err := s.mail.send(email, subject, body); err != nil {
			log.Printf("otp mail %s: %v", s.logID(email), err)
			return
		}
		log.Printf("otp sent %s", s.logID(email))
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// codeEmails holds the plain-text code email per supported locale; %s is the code.
var codeEmails = map[string][2]string{
	"en-US": {"Your Tolon poll code", "Your code is %s. It expires in 10 minutes. If you did not ask for it, ignore this email.\n"},
	"pt-BR": {"Seu código da votação do Tolon", "Seu código é %s. Ele expira em 10 minutos. Se você não pediu este código, ignore este email.\n"},
	"pt-PT": {"O seu código da votação do Tolon", "O seu código é %s. Expira dentro de 10 minutos. Se não pediu este código, ignore este email.\n"},
}

func init() { codeEmails["en-GB"] = codeEmails["en-US"] }

// codeEmail returns subject and body for locale, falling back to en-US.
func codeEmail(locale, code string) (string, string) {
	t, ok := codeEmails[locale]
	if !ok {
		t = codeEmails["en-US"]
	}
	return t[0], fmt.Sprintf(t[1], code)
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
