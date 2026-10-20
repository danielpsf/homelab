package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testAud    = "test-aud"
	testOrigin = "https://tolon.example.test"
	testAdmin  = "admin@example.test"
	testSecret = "0123456789abcdef0123456789abcdef"
)

var (
	testKey, otherKey *rsa.PrivateKey
	jsonPost          = map[string]string{"Content-Type": "application/json", "Origin": testOrigin}
	codeRe            = regexp.MustCompile(`\b\d{6}\b`)
)

var _ = BeforeSuite(func() {
	log.SetOutput(GinkgoWriter) // app logs show only for failing specs
	var err error
	testKey, err = rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	otherKey, err = rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
})

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// sign builds an RS256 JWT the way Cloudflare Access does.
func sign(key *rsa.PrivateKey, kid string, claims map[string]any) string {
	GinkgoHelper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	Expect(err).NotTo(HaveOccurred())
	return in + "." + b64(sig)
}

func jwk(kid string, key *rsa.PrivateKey) map[string]string {
	return map[string]string{"kid": kid, "kty": "RSA", "alg": "RS256",
		"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}
}

type sentMail struct{ to, subject, body string }

// fakeMail records every mail; fail, when set, is returned after recording.
type fakeMail struct {
	sent chan sentMail
	fail error
}

func (f *fakeMail) send(to, subject, body string) error {
	f.sent <- sentMail{to, subject, body}
	return f.fail
}

// env is one isolated server: its own database, web dir, fake Access team and fake Turnstile.
type env struct {
	s        *server
	h        http.Handler
	mail     *fakeMail
	now      time.Time
	certHits atomic.Int32
	certKeys atomic.Value // []map[string]string served by the fake team domain
}

func newEnv() *env {
	GinkgoHelper()
	e := &env{now: time.Now(), mail: &fakeMail{sent: make(chan sentMail, 64)}}
	e.certKeys.Store([]map[string]string{jwk("k1", testKey)})
	team := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.certHits.Add(1)
		time.Sleep(20 * time.Millisecond) // long enough for concurrent callers to pile up
		json.NewEncoder(w).Encode(map[string]any{"keys": e.certKeys.Load()})
	}))
	DeferCleanup(team.Close)
	siteverify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		ok := r.Form.Get("secret") == "ts-secret" && r.Form.Get("response") == "good"
		json.NewEncoder(w).Encode(map[string]any{"success": ok, "hostname": "tolon.example.test"})
	}))
	DeferCleanup(siteverify.Close)

	web := GinkgoT().TempDir()
	for name, body := range map[string]string{
		"options.json": `[{"id":"lon","name":"Lon"},{"id":"tol","name":"Tol"}]`,
		"index.html":   "<p>landing</p>",
		"pool.html":    "<p>pool</p>",
		"results.html": "<p>results</p>",
	} {
		Expect(os.WriteFile(filepath.Join(web, name), []byte(body), 0o644)).To(Succeed())
	}
	Expect(os.Mkdir(filepath.Join(web, "assets"), 0o755)).To(Succeed())

	db, err := openDB(filepath.Join(GinkgoT().TempDir(), "poll.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(db.Close)
	e.s = newServer(db, team.URL, testAud, testOrigin, testAdmin, testSecret, web)
	e.s.captcha = &turnstile{secret: "ts-secret", verifyURL: siteverify.URL, hostname: "tolon.example.test", client: http.DefaultClient}
	e.s.sitekey = "site-key"
	e.s.mail = e.mail
	e.s.now = func() time.Time { return e.now }
	Expect(e.s.auth.refresh()).To(Succeed())
	e.h = e.s.routes()
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

// tok signs an Access JWT for the admin app; mod adjusts the claims.
func (e *env) tok(email string, mod func(map[string]any)) string {
	GinkgoHelper()
	now := time.Now().Unix()
	c := map[string]any{"aud": []string{testAud}, "iss": e.s.auth.issuer, "email": email, "exp": now + 600, "nbf": now - 10}
	if mod != nil {
		mod(c)
	}
	return sign(testKey, "k1", c)
}

func (e *env) jwt(token string) map[string]string {
	return map[string]string{"Cf-Access-Jwt-Assertion": token}
}

func (e *env) askCode(email string, hdr ...map[string]string) *httptest.ResponseRecorder {
	h := jsonPost
	if len(hdr) > 0 {
		h = hdr[0]
	}
	return e.do("POST", "/api/otp/request", `{"email":"`+email+`","turnstile":"good"}`, h)
}

// requestCode asks for a code and returns the one the fake mailer received.
func (e *env) requestCode(email string) string {
	GinkgoHelper()
	rec := e.askCode(email)
	Expect(rec).To(HaveHTTPStatus(http.StatusAccepted))
	Expect(rec.Body.String()).To(MatchJSON(`{"ok":true}`))
	var m sentMail
	Eventually(e.mail.sent).Should(Receive(&m))
	code := codeRe.FindString(m.body)
	Expect(code).NotTo(BeEmpty())
	return code
}

func (e *env) verify(email, code string) *httptest.ResponseRecorder {
	return e.do("POST", "/api/otp/verify", `{"email":"`+email+`","code":"`+code+`"}`, jsonPost)
}

func (e *env) login(email string) *http.Cookie {
	GinkgoHelper()
	rec := e.verify(email, e.requestCode(email))
	Expect(rec).To(HaveHTTPStatus(http.StatusOK))
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	Fail("no session cookie")
	return nil
}

func (e *env) me(hdr map[string]string, cookies ...*http.Cookie) map[string]any {
	GinkgoHelper()
	rec := e.do("GET", "/api/me", "", hdr, cookies...)
	Expect(rec).To(HaveHTTPStatus(http.StatusOK))
	var m map[string]any
	Expect(json.Unmarshal(rec.Body.Bytes(), &m)).To(Succeed())
	return m
}

func (e *env) count(table string) int {
	GinkgoHelper()
	var n int
	Expect(e.s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)).To(Succeed())
	return n
}

func writeI18n(dir string, files map[string]string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Join(dir, "i18n"), 0o755)).To(Succeed())
	for name, body := range files {
		Expect(os.WriteFile(filepath.Join(dir, "i18n", name+".json"), []byte(body), 0o644)).To(Succeed())
	}
}

// syncBuffer is a log sink that is safe to read while the mail goroutine writes.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
