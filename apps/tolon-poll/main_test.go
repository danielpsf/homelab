package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testAud    = "test-aud"
	testOrigin = "https://tolon.example.test"
	testAdmin  = "admin@example.test"
	testSecret = "0123456789abcdef0123456789abcdef"
)

var testKey, otherKey *rsa.PrivateKey

func init() {
	testKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	otherKey, _ = rsa.GenerateKey(rand.Reader, 2048)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + b64(sig)
}

type fakeMail struct {
	sent     chan [2]string
	subjects chan string
}

func (f *fakeMail) send(to, subject, body string) error {
	select {
	case f.subjects <- subject:
	default:
	}
	f.sent <- [2]string{to, body}
	return nil
}

type env struct {
	s    *server
	h    http.Handler
	mail *fakeMail
	t    *testing.T
	now  time.Time
}

// tok signs an Access JWT for the admin app; mod adjusts claims.
func (e *env) tok(email string, mod func(map[string]any)) string {
	now := time.Now().Unix()
	c := map[string]any{"aud": []string{testAud}, "iss": e.s.auth.issuer, "email": email, "exp": now + 600, "nbf": now - 10}
	if mod != nil {
		mod(c)
	}
	return sign(e.t, testKey, "k1", c)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	team := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA", "alg": "RS256",
			"n": b64(testKey.N.Bytes()), "e": b64(big.NewInt(int64(testKey.E)).Bytes()),
		}}})
	}))
	t.Cleanup(team.Close)
	siteverify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		ok := r.Form.Get("secret") == "ts-secret" && r.Form.Get("response") == "good"
		json.NewEncoder(w).Encode(map[string]any{"success": ok, "hostname": "tolon.example.test"})
	}))
	t.Cleanup(siteverify.Close)

	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "options.json"), []byte(`[{"id":"lon","name":"Lon"},{"id":"tol","name":"Tol"}]`), 0o644)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<p>landing</p>"), 0o644)
	os.WriteFile(filepath.Join(web, "pool.html"), []byte("<p>pool</p>"), 0o644)
	os.WriteFile(filepath.Join(web, "results.html"), []byte("<p>results</p>"), 0o644)
	os.Mkdir(filepath.Join(web, "assets"), 0o755)

	db, err := openDB(filepath.Join(t.TempDir(), "poll.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := newServer(db, team.URL, testAud, testOrigin, testAdmin, testSecret, web)
	s.captcha = &turnstile{secret: "ts-secret", verifyURL: siteverify.URL, hostname: "tolon.example.test", client: http.DefaultClient}
	s.sitekey = "site-key"
	e := &env{s: s, mail: &fakeMail{sent: make(chan [2]string, 64), subjects: make(chan string, 64)}, t: t, now: time.Now()}
	s.mail = e.mail
	s.now = func() time.Time { return e.now }
	if err := s.auth.refresh(); err != nil {
		t.Fatal(err)
	}
	e.h = s.routes()
	return e
}

func (e *env) do(method, path, body string, hdr map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

var jsonPost = map[string]string{"Content-Type": "application/json", "Origin": testOrigin}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

// requestCode asks for a code and returns the one the fake mailer received.
func (e *env) requestCode(email string) string {
	e.t.Helper()
	rec := e.do("POST", "/api/otp/request", `{"email":"`+email+`","turnstile":"good"}`, jsonPost)
	if rec.Code != 202 || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		e.t.Fatalf("otp request: %d %s", rec.Code, rec.Body)
	}
	select {
	case m := <-e.mail.sent:
		return codeRe.FindString(m[1])
	case <-time.After(2 * time.Second):
		e.t.Fatal("no mail sent")
		return ""
	}
}

func (e *env) verify(email, code string) *httptest.ResponseRecorder {
	return e.do("POST", "/api/otp/verify", `{"email":"`+email+`","code":"`+code+`"}`, jsonPost)
}

func (e *env) login(email string) *http.Cookie {
	e.t.Helper()
	rec := e.verify(email, e.requestCode(email))
	if rec.Code != 200 {
		e.t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	e.t.Fatal("no session cookie")
	return nil
}

func TestOTPLoginAndCookieFlags(t *testing.T) {
	e := newEnv(t)
	code := e.requestCode(" Voter@Example.TEST ")
	rec := e.verify("voter@example.test", code)
	if rec.Code != 200 {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
	raw := rec.Header().Get("Set-Cookie")
	for _, want := range []string{sessionCookie + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict", "Max-Age=86400"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("cookie %q lacks %q", raw, want)
		}
	}
	if strings.Contains(raw, "Domain=") {
		t.Fatalf("__Host- cookie must not set Domain: %q", raw)
	}
	c := rec.Result().Cookies()[0]
	if len(c.Value) != 64 {
		t.Fatalf("session id length %d", len(c.Value))
	}
	var stored int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, c.Value).Scan(&stored)
	if stored != 0 {
		t.Fatal("session id stored in plain")
	}
	var me map[string]any
	json.Unmarshal(e.do("GET", "/api/me", "", nil, c).Body.Bytes(), &me)
	if me["email"] != "voter@example.test" || me["voted"] != false || me["is_admin"] != false {
		t.Fatalf("me: %v", me)
	}
	// session expires after 24 h
	e.now = e.now.Add(sessionTTL + time.Second)
	json.Unmarshal(e.do("GET", "/api/me", "", nil, c).Body.Bytes(), &me)
	if me["email"] != nil {
		t.Fatalf("expired session still valid: %v", me)
	}
}

func TestCodeSingleUseAndExpiry(t *testing.T) {
	e := newEnv(t)
	code := e.requestCode("a@example.test")
	if rec := e.verify("a@example.test", code); rec.Code != 200 {
		t.Fatalf("first use: %d", rec.Code)
	}
	if rec := e.verify("a@example.test", code); rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_code") {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body)
	}
	code = e.requestCode("b@example.test")
	e.now = e.now.Add(codeTTL + time.Second)
	if rec := e.verify("b@example.test", code); rec.Code != 400 {
		t.Fatalf("expired code accepted: %d", rec.Code)
	}
	// a new request replaces the old code
	e.now = time.Now()
	old := e.requestCode("c@example.test")
	fresh := e.requestCode("c@example.test")
	if old != fresh {
		if rec := e.verify("c@example.test", old); rec.Code != 400 {
			t.Fatalf("replaced code accepted: %d", rec.Code)
		}
	}
	if rec := e.verify("c@example.test", fresh); rec.Code != 200 {
		t.Fatalf("fresh code: %d", rec.Code)
	}
}

func TestAttemptLimitAndHMACStorage(t *testing.T) {
	e := newEnv(t)
	code := e.requestCode("d@example.test")
	var stored, key string
	e.s.db.QueryRow(`SELECT email_key, code_mac FROM otp_codes`).Scan(&key, &stored)
	if strings.Contains(stored, code) || strings.Contains(key, "d@example.test") || stored != e.s.mac("code", "d@example.test", code) {
		t.Fatalf("code not HMAC-stored: %q", stored)
	}
	var plain int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM otp_sends WHERE email_key LIKE '%@%'`).Scan(&plain)
	if plain != 0 {
		t.Fatal("plain email in otp_sends")
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < maxCodeAttempts; i++ {
		if rec := e.verify("d@example.test", wrong); rec.Code != 400 {
			t.Fatalf("wrong attempt %d: %d", i, rec.Code)
		}
	}
	if rec := e.verify("d@example.test", code); rec.Code != 400 {
		t.Fatalf("burnt code accepted: %d", rec.Code)
	}
}

func TestOTPRequestRevealsNothingAndLimits(t *testing.T) {
	e := newEnv(t)
	c := e.login("voted@example.test")
	if rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, c); rec.Code != 201 {
		t.Fatalf("vote: %d %s", rec.Code, rec.Body)
	}
	a := e.do("POST", "/api/otp/request", `{"email":"voted@example.test","turnstile":"good"}`, jsonPost)
	b := e.do("POST", "/api/otp/request", `{"email":"fresh@example.test","turnstile":"good"}`, jsonPost)
	<-e.mail.sent
	<-e.mail.sent
	if a.Code != 202 || b.Code != 202 || a.Body.String() != b.Body.String() {
		t.Fatalf("responses differ: %d %q vs %d %q", a.Code, a.Body, b.Code, b.Body)
	}
	// 3 codes per email per hour, persisted in SQLite
	e.requestCode("lim@example.test")
	e.requestCode("lim@example.test")
	e.requestCode("lim@example.test")
	if rec := e.do("POST", "/api/otp/request", `{"email":"lim@example.test","turnstile":"good"}`, jsonPost); rec.Code != 429 {
		t.Fatalf("4th code: %d", rec.Code)
	}
	e.s.otpIPLimit = newLimiter(1000, time.Hour)
	restarted := newServer(e.s.db, e.s.auth.issuer, testAud, testOrigin, testAdmin, testSecret, e.s.webDir)
	restarted.captcha, restarted.mail, restarted.now = e.s.captcha, e.s.mail, e.s.now
	e.h = restarted.routes()
	if rec := e.do("POST", "/api/otp/request", `{"email":"lim@example.test","turnstile":"good"}`, jsonPost); rec.Code != 429 {
		t.Fatalf("limit lost on restart: %d", rec.Code)
	}
	// 10 per IP per hour
	e.h = e.s.routes()
	e.s.otpIPLimit = newLimiter(10, time.Hour)
	ip := map[string]string{"Content-Type": "application/json", "Origin": testOrigin, "CF-Connecting-IP": "198.51.100.1"}
	for i := 0; i < 10; i++ {
		e.do("POST", "/api/otp/request", `{"email":"ip`+string(rune('a'+i))+`@example.test","turnstile":"good"}`, ip)
		<-e.mail.sent
	}
	if rec := e.do("POST", "/api/otp/request", `{"email":"ipz@example.test","turnstile":"good"}`, ip); rec.Code != 429 {
		t.Fatalf("11th per IP: %d", rec.Code)
	}
	// invalid email
	if rec := e.do("POST", "/api/otp/request", `{"email":"not an email","turnstile":"good"}`, jsonPost); rec.Code != 400 {
		t.Fatalf("bad email: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/otp/request", `{"email":"`+strings.Repeat("a", 250)+`@x.io","turnstile":"good"}`, jsonPost); rec.Code != 400 {
		t.Fatalf("long email: %d", rec.Code)
	}
}

func TestPerEmailDailyCap(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.requestCode("day@example.test")
	}
	e.now = e.now.Add(61 * time.Minute)
	e.requestCode("day@example.test")
	e.requestCode("day@example.test")
	e.now = e.now.Add(61 * time.Minute)
	if rec := e.do("POST", "/api/otp/request", `{"email":"day@example.test","turnstile":"good"}`, jsonPost); rec.Code != 429 {
		t.Fatalf("6th code in a day: %d", rec.Code)
	}
}

func TestLockoutAfterBurntCodes(t *testing.T) {
	e := newEnv(t)
	e.s.otpIPLimit = newLimiter(1000, time.Hour)
	var code string
	for i := 0; i < burnsToLockout; i++ {
		code = e.requestCode("lock@example.test")
		for j := 0; j < maxCodeAttempts; j++ {
			e.verify("lock@example.test", "x")
		}
	}
	e.now = e.now.Add(61 * time.Minute) // past the hourly limit, still inside the lockout
	if rec := e.do("POST", "/api/otp/request", `{"email":"lock@example.test","turnstile":"good"}`, jsonPost); rec.Code != 429 {
		t.Fatalf("request while locked: %d", rec.Code)
	}
	if rec := e.verify("lock@example.test", code); rec.Code != 400 {
		t.Fatalf("verify while locked: %d", rec.Code)
	}
	e.now = e.now.Add(lockoutFor)
	e.s.prune()
	if c := e.requestCode("lock@example.test"); e.verify("lock@example.test", c).Code != 200 {
		t.Fatal("still locked after 24 h")
	}
}

func TestClientIPKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":                "203.0.113.7",
		"::ffff:203.0.113.7":         "203.0.113.7",
		"2001:db8:1:2:aaaa::1":       "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::99": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":            "2001:db8:1:3::/64",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("CF-Connecting-IP", in)
		if got := clientIP(r); got != want {
			t.Fatalf("clientIP(%s) = %s want %s", in, got, want)
		}
	}
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "2001:db8:5:5::" + strconv.Itoa(i+1)})
	}
	if rec := e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "2001:db8:5:5:ffff::1"}); rec.Code != 429 {
		t.Fatalf("same /64 not limited together: %d", rec.Code)
	}
}

func TestGlobalDailyCap(t *testing.T) {
	e := newEnv(t)
	e.s.codesPerDay = 10
	for i := 0; i < e.s.codesPerDay; i++ {
		e.s.db.Exec(`INSERT INTO otp_sends (email_key, sent_at) VALUES (?, ?)`, "k", e.now.Unix())
	}
	if rec := e.do("POST", "/api/otp/request", `{"email":"x@example.test","turnstile":"good"}`, jsonPost); rec.Code != 429 {
		t.Fatalf("over daily cap: %d", rec.Code)
	}
}

func TestTurnstileRejected(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"bad", ""} {
		rec := e.do("POST", "/api/otp/request", `{"email":"t@example.test","turnstile":"`+tok+`"}`, jsonPost)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "turnstile_failed") {
			t.Fatalf("turnstile %q: %d %s", tok, rec.Code, rec.Body)
		}
	}
	e.s.captcha.hostname = "other.example"
	if rec := e.do("POST", "/api/otp/request", `{"email":"t@example.test","turnstile":"good"}`, jsonPost); rec.Code != 400 {
		t.Fatalf("hostname mismatch accepted: %d", rec.Code)
	}
	var n int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM otp_codes`).Scan(&n)
	if n != 0 || len(e.mail.sent) != 0 {
		t.Fatal("code issued despite Turnstile failure")
	}
}

func TestCodeEmailLocale(t *testing.T) {
	e := newEnv(t)
	e.s.otpIPLimit = newLimiter(1000, time.Hour)
	for i, tc := range []struct{ locale, subject, body string }{
		{"pt-BR", "Seu código da votação do Tolon", "Seu código é "},
		{"pt-PT", "O seu código da votação do Tolon", "O seu código é "},
		{"en-GB", "Your Tolon poll code", "Your code is "},
		{"fr-FR", "Your Tolon poll code", "Your code is "},
		{"", "Your Tolon poll code", "Your code is "},
	} {
		email := "loc" + strconv.Itoa(i) + "@example.test"
		rec := e.do("POST", "/api/otp/request", `{"email":"`+email+`","turnstile":"good","locale":"`+tc.locale+`"}`, jsonPost)
		if rec.Code != 202 {
			t.Fatalf("%q: %d", tc.locale, rec.Code)
		}
		m := <-e.mail.sent
		subj := <-e.mail.subjects
		if subj != tc.subject || !strings.HasPrefix(m[1], tc.body) || !codeRe.MatchString(m[1]) || strings.Contains(m[1], "http") {
			t.Fatalf("%q: %q %q", tc.locale, subj, m[1])
		}
	}
}

func TestSMTPErrClassHidesAddress(t *testing.T) {
	cases := map[error]string{
		&textproto.Error{Code: 550, Msg: "5.1.1 <victim@example.test>: Recipient address rejected"}:         "smtp reply 550 (class 5xx)",
		fmt.Errorf("rcpt: %w", &textproto.Error{Code: 452, Msg: "too many recipients victim@example.test"}): "smtp reply 452 (class 4xx)",
		errors.New("smtp: victim@example.test refused"):                                                     "error *errors.errorString",
	}
	for err, want := range cases {
		if got := smtpErrClass(err); got != want || strings.Contains(got, "@") {
			t.Fatalf("smtpErrClass(%v) = %q want %q", err, got, want)
		}
	}
}

func TestSecretEnv(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s")
	os.WriteFile(f, []byte("from-file\n"), 0o600)
	t.Setenv("TP_SECRET", "from-env")
	if got := secretEnv("TP_SECRET"); got != "from-env" {
		t.Fatalf("plain env fallback: %q", got)
	}
	t.Setenv("TP_SECRET_FILE", f)
	if got := secretEnv("TP_SECRET"); got != "from-file" {
		t.Fatalf("file wins: %q", got)
	}
}

func TestEmailUnavailable(t *testing.T) {
	e := newEnv(t)
	e.s.mail = nil
	rec := e.do("POST", "/api/otp/request", `{"email":"t@example.test","turnstile":"good"}`, jsonPost)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "email_unavailable") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestVote(t *testing.T) {
	e := newEnv(t)
	body := `{"favourite":"lon","reactions":{"lon":"love","tol":"no"},"comment":"nice\u0007 one"}`
	if rec := e.do("POST", "/api/vote", body, jsonPost); rec.Code != 401 {
		t.Fatalf("vote without session: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/vote", body, jsonPost, &http.Cookie{Name: sessionCookie, Value: strings.Repeat("a", 64)}); rec.Code != 401 {
		t.Fatalf("vote with forged session: %d", rec.Code)
	}
	c := e.login("voter@example.test")
	if rec := e.do("POST", "/api/vote", body, jsonPost, c); rec.Code != 201 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("first vote: %d %s", rec.Code, rec.Body)
	}
	c2 := e.login("VOTER@example.test") // second session, same email
	if rec := e.do("POST", "/api/vote", body, jsonPost, c2); rec.Code != 409 || !strings.Contains(rec.Body.String(), "already_voted") {
		t.Fatalf("second vote: %d %s", rec.Code, rec.Body)
	}
	var me map[string]any
	json.Unmarshal(e.do("GET", "/api/me", "", nil, c).Body.Bytes(), &me)
	if me["voted"] != true {
		t.Fatalf("me after vote: %v", me)
	}
	var comment string
	e.s.db.QueryRow(`SELECT comment FROM votes`).Scan(&comment)
	if comment != "nice one" {
		t.Fatalf("control chars not stripped: %q", comment)
	}
	// logout ends the session
	if rec := e.do("POST", "/api/logout", `{}`, jsonPost, c); rec.Code != 200 || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %s", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	json.Unmarshal(e.do("GET", "/api/me", "", nil, c).Body.Bytes(), &me)
	if me["email"] != nil {
		t.Fatalf("session survived logout: %v", me)
	}
}

func TestVoteValidation(t *testing.T) {
	e := newEnv(t)
	e.s.voteLimit = newLimiter(100, time.Hour)
	c := e.login("v@example.test")
	bad := map[string]string{
		"missing favourite":   `{"reactions":{}}`,
		"unknown favourite":   `{"favourite":"nope"}`,
		"unknown reaction id": `{"favourite":"lon","reactions":{"zzz":"love"}}`,
		"bad reaction value":  `{"favourite":"lon","reactions":{"lon":"meh"}}`,
		"long comment":        `{"favourite":"lon","comment":"` + strings.Repeat("a", 1001) + `"}`,
		"unknown field":       `{"favourite":"lon","ip":"1.2.3.4"}`,
		"not json":            `favourite=lon`,
		"trailing data":       `{"favourite":"lon"}{"favourite":"tol"}`,
		"too big":             `{"favourite":"lon","comment":"` + strings.Repeat("a", 17<<10) + `"}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			rec := e.do("POST", "/api/vote", body, jsonPost, c)
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"invalid"`) {
				t.Fatalf("got %d %s", rec.Code, rec.Body)
			}
		})
	}
	ok := `{"favourite":"tol","comment":"` + strings.Repeat("é", 1000) + `"}`
	if rec := e.do("POST", "/api/vote", ok, jsonPost, c); rec.Code != 201 {
		t.Fatalf("1000-char comment: %d %s", rec.Code, rec.Body)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	c := e.login("c@example.test")
	cases := map[string]map[string]string{
		"no origin":    {"Content-Type": "application/json"},
		"other origin": {"Content-Type": "application/json", "Origin": "https://evil.example"},
		"http origin":  {"Content-Type": "application/json", "Origin": "http://tolon.example.test"},
		"form encoded": {"Content-Type": "application/x-www-form-urlencoded", "Origin": testOrigin},
		"text plain":   {"Content-Type": "text/plain", "Origin": testOrigin},
	}
	for name, hdr := range cases {
		t.Run(name, func(t *testing.T) {
			for _, p := range []string{"/api/vote", "/api/otp/request", "/api/otp/verify", "/api/logout"} {
				if rec := e.do("POST", p, `{"favourite":"lon"}`, hdr, c); rec.Code != 400 && rec.Code != 403 {
					t.Fatalf("%s: got %d", p, rec.Code)
				}
			}
		})
	}
	var n int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM votes`).Scan(&n)
	if n != 0 {
		t.Fatalf("CSRF request stored a vote")
	}
	if rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, map[string]string{"Content-Type": "application/json; charset=utf-8", "Origin": testOrigin}, c); rec.Code != 201 {
		t.Fatalf("valid: %d", rec.Code)
	}
}

func TestAdminJWT(t *testing.T) {
	e := newEnv(t)
	now := time.Now().Unix()
	iss := e.s.auth.issuer
	cases := map[string]struct {
		token string
		want  int
	}{
		"valid admin":   {e.tok("Admin@Example.test", nil), 200},
		"aud as string": {e.tok(testAdmin, func(c map[string]any) { c["aud"] = testAud }), 200},
		"non-admin":     {e.tok("someone@example.test", nil), 403},
		"missing":       {"", 401},
		"garbage":       {"a.b.c", 401},
		"wrong aud":     {e.tok(testAdmin, func(c map[string]any) { c["aud"] = "other" }), 401},
		"wrong iss":     {e.tok(testAdmin, func(c map[string]any) { c["iss"] = "https://evil.cloudflareaccess.com" }), 401},
		"expired":       {e.tok(testAdmin, func(c map[string]any) { c["exp"] = now - 3600 }), 401},
		"no exp":        {e.tok(testAdmin, func(c map[string]any) { delete(c, "exp") }), 401},
		"not yet valid": {e.tok(testAdmin, func(c map[string]any) { c["nbf"] = now + 3600 }), 401},
		"bad signature": {sign(t, otherKey, "k1", map[string]any{"aud": testAud, "iss": iss, "email": testAdmin, "exp": now + 60}), 401},
		"unknown kid":   {sign(t, testKey, "k9", map[string]any{"aud": testAud, "iss": iss, "email": testAdmin, "exp": now + 60}), 401},
		"alg none":      {b64([]byte(`{"alg":"none","kid":"k1"}`)) + "." + b64([]byte(`{"aud":"test-aud","email":"admin@example.test"}`)) + ".", 401},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for _, p := range []string{"/api/results", "/pool/results"} {
				if got := e.do("GET", p, "", map[string]string{"Cf-Access-Jwt-Assertion": tc.token}).Code; got != tc.want {
					t.Fatalf("%s: got %d want %d", p, got, tc.want)
				}
			}
		})
	}
	if rec := e.do("GET", "/api/results", "", map[string]string{"Cf-Access-Authenticated-User-Email": testAdmin}); rec.Code != 401 {
		t.Fatalf("email header without JWT: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/results", "", nil, &http.Cookie{Name: "CF_Authorization", Value: e.tok(testAdmin, nil)}); rec.Code != 200 {
		t.Fatalf("cookie JWT: %d", rec.Code)
	}
	// a voter session is not admin
	if rec := e.do("GET", "/api/results", "", nil, e.login(testAdmin)); rec.Code != 401 {
		t.Fatalf("session as admin: %d", rec.Code)
	}
	for _, p := range []string{"/results.html", "/results"} {
		if rec := e.do("GET", p, "", nil); rec.Code != 404 {
			t.Fatalf("%s direct: %d", p, rec.Code)
		}
	}
	// is_admin only with a valid admin Access JWT
	var me map[string]any
	json.Unmarshal(e.do("GET", "/api/me", "", map[string]string{"Cf-Access-Jwt-Assertion": e.tok(testAdmin, nil)}).Body.Bytes(), &me)
	if me["is_admin"] != true {
		t.Fatalf("admin JWT on /api/me: %v", me)
	}
	json.Unmarshal(e.do("GET", "/api/me", "", map[string]string{"Cf-Access-Jwt-Assertion": e.tok("x@example.test", nil)}).Body.Bytes(), &me)
	if me["is_admin"] != false {
		t.Fatalf("non-admin JWT on /api/me: %v", me)
	}
	// results content
	e.s.db.Exec(`INSERT INTO votes (email, favourite, reactions, comment, created_at) VALUES ('v@x.test','lon','{"tol":"no"}','hi','2026-01-01T00:00:00Z')`)
	var res struct {
		Total      int
		Favourites map[string]int
		Reactions  map[string]map[string]int
		Comments   []map[string]string
	}
	json.Unmarshal(e.do("GET", "/api/results", "", map[string]string{"Cf-Access-Jwt-Assertion": e.tok(testAdmin, nil)}).Body.Bytes(), &res)
	if res.Total != 1 || res.Favourites["lon"] != 1 || res.Reactions["tol"]["no"] != 1 || res.Comments[0]["email"] != "v@x.test" || res.Comments[0]["at"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("results: %+v", res)
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.login("r@example.test")
	for i := 0; i < 5; i++ {
		if rec := e.do("POST", "/api/vote", `{}`, jsonPost, c); rec.Code != 400 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, c); rec.Code != 429 || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("6th vote attempt: %d %s", rec.Code, rec.Body)
	}
	ip := map[string]string{"CF-Connecting-IP": "203.0.113.7"}
	for i := 0; i < 30; i++ {
		if rec := e.do("GET", "/api/options", "", ip); rec.Code != 200 {
			t.Fatalf("req %d: %d", i, rec.Code)
		}
	}
	if rec := e.do("GET", "/api/options", "", ip); rec.Code != 429 {
		t.Fatalf("31st api: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "203.0.113.8"}); rec.Code != 200 {
		t.Fatalf("other IP: %d", rec.Code)
	}
	host := map[string]string{"CF-Connecting-IP": "203.0.113.9"}
	for i := 0; i < 120; i++ {
		e.do("GET", "/pool", "", host)
	}
	if rec := e.do("GET", "/pool", "", host); rec.Code != 429 {
		t.Fatalf("host limit: %d", rec.Code)
	}
	l := newLimiter(1, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.allow("k") || l.allow("k") {
		t.Fatal("bucket of 1")
	}
	now = now.Add(61 * time.Second)
	if !l.allow("k") {
		t.Fatal("no refill")
	}
	l.allow("other")
	now = now.Add(30 * time.Second) // both buckets half refilled
	l.evict()
	if _, ok := l.buckets["k"]; !ok {
		t.Fatal("evicted a bucket that is still draining")
	}
	now = now.Add(time.Minute)
	l.evict()
	if len(l.buckets) != 0 {
		t.Fatalf("%d refilled buckets kept", len(l.buckets))
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t)
	e.s.contact = "poll@example.test"
	if rec := e.do("GET", "/api/config", "", nil); !strings.Contains(rec.Body.String(), `"delete_after":null`) {
		t.Fatalf("config without deadline: %s", rec.Body)
	}
	e.s.closeAt = time.Date(2027, 1, 18, 23, 59, 59, 0, time.UTC)
	rec := e.do("GET", "/api/config", "", nil)
	var cfg map[string]any
	json.Unmarshal(rec.Body.Bytes(), &cfg)
	if cfg["delete_after"] != "2027-01-18T23:59:59Z" || cfg["contact_email"] != "poll@example.test" ||
		cfg["turnstile_sitekey"] != "site-key" || len(cfg["processors"].([]any)) != 2 {
		t.Fatalf("config: %s", rec.Body)
	}
	c := e.login("keep@example.test")
	if rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, c); rec.Code != 201 {
		t.Fatalf("vote: %d", rec.Code)
	}
	e.requestCode("pending@example.test")
	count := func() (n int) {
		for _, tb := range []string{"votes", "sessions", "otp_codes", "otp_sends", "otp_burns", "otp_lockouts"} {
			var k int
			e.s.db.QueryRow(`SELECT COUNT(*) FROM ` + tb).Scan(&k)
			n += k
		}
		return n
	}
	e.s.closeAt = e.now.Add(time.Hour)
	e.s.prune()
	if count() == 0 {
		t.Fatal("data deleted before the deadline")
	}
	e.now = e.now.Add(time.Hour + time.Second)
	e.s.prune()
	if n := count(); n != 0 {
		t.Fatalf("%d rows left after the deadline", n)
	}
	if rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, c); rec.Code != 410 {
		t.Fatalf("vote after close: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/otp/request", `{"email":"x@example.test","turnstile":"good"}`, jsonPost); rec.Code != 410 {
		t.Fatalf("code after close: %d", rec.Code)
	}
}

func TestPublicRoutesAndHeaders(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("GET", "/", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "landing") {
		t.Fatalf("landing: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/pool", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "pool") {
		t.Fatalf("pool: %d", rec.Code)
	}
	if rec := e.do("GET", "/assets/", "", nil); rec.Code != 404 {
		t.Fatalf("dir listing: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/config", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"turnstile_sitekey":"site-key"`) {
		t.Fatalf("config: %d %s", rec.Code, rec.Body)
	}
	var me map[string]any
	rec := e.do("GET", "/api/me", "", nil)
	json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != 200 || me["email"] != nil || me["voted"] != false {
		t.Fatalf("anonymous me: %d %v", rec.Code, me)
	}
	rec = e.do("GET", "/api/options", "", nil)
	var opts []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &opts)
	if rec.Code != 200 || len(opts) != 2 {
		t.Fatalf("options: %d %s", rec.Code, rec.Body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self' https://challenges.cloudflare.com", "frame-src https://challenges.cloudflare.com", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q lacks %q", csp, want)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(rec.Header().Get("Permissions-Policy"), "camera=()") {
		t.Fatalf("headers missing: %v", rec.Header())
	}
	if rec := e.do("GET", "/healthz", "", nil); rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
	for in, want := range map[string]string{" A@B.io ": "a@b.io", "a@b": "", "a b@c.io": "", "a@b..io": "", "a@-b.io": "", "x\r\n@b.io": ""} {
		if got := normaliseEmail(in); got != want {
			t.Fatalf("normaliseEmail(%q) = %q want %q", in, got, want)
		}
	}
}
