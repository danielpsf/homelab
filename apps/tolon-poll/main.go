// Command tolon-poll serves the Tolon icon poll: email one-time codes for voters,
// Cloudflare Access (verified JWT) for the admin results.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	maxBody    = 16 << 10
	maxComment = 1000
)

var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; " +
		"frame-src https://challenges.cloudflare.com; connect-src 'self'; img-src 'self' data:; " +
		"object-src 'none'; frame-ancestors 'none'; base-uri 'none'",
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"X-Frame-Options":        "DENY",
	"Permissions-Policy": "accelerometer=(), ambient-light-sensor=(), autoplay=(), battery=(), bluetooth=(), camera=(), " +
		"clipboard-read=(), clipboard-write=(), display-capture=(), document-domain=(), encrypted-media=(), " +
		"fullscreen=(), gamepad=(), geolocation=(), gyroscope=(), hid=(), idle-detection=(), interest-cohort=(), " +
		"magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), " +
		"screen-wake-lock=(), serial=(), sync-xhr=(), usb=(), web-share=(), xr-spatial-tracking=()",
}

type server struct {
	db          *sql.DB
	auth        *verifier // Cloudflare Access JWT, admin paths only
	captcha     *turnstile
	mail        mailer // nil when SMTP is not configured
	secret      []byte
	sitekey     string
	contact     string    // privacy contact address, served to the page
	codesPerDay int       // global cap on code emails per 24 h, persistent
	closeAt     time.Time // POLL_DELETE_AFTER: votes close and all personal data is deleted after it
	webDir      string
	origin      string
	admin       string
	now         func() time.Time

	hostLimit  *limiter // every request, per IP
	apiLimit   *limiter // /api/*, per IP
	otpIPLimit *limiter // code requests, per IP
	voteLimit  *limiter // vote attempts, per email
}

func main() {
	health := flag.Bool("healthcheck", false, "probe the local server and exit")
	flag.Parse()
	addr := envOr("LISTEN_ADDR", ":8080")
	if *health {
		c := &http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get("http://127.0.0.1" + addr[strings.LastIndex(addr, ":"):] + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := map[string]string{}
	for _, k := range []string{"CF_ACCESS_TEAM_DOMAIN", "CF_ACCESS_AUD", "PUBLIC_ORIGIN", "ADMIN_EMAIL", "SESSION_SECRET"} {
		if cfg[k] = secretEnv(k); cfg[k] == "" {
			log.Fatalf("missing env %s", k)
		}
	}
	if len(cfg["SESSION_SECRET"]) < 32 {
		log.Fatal("SESSION_SECRET must be at least 32 characters")
	}
	syscall.Umask(0o077) // database, WAL and SHM files are created owner-only
	db, err := openDB(envOr("DB_PATH", "/data/poll.db"))
	if err != nil {
		log.Fatal(err)
	}
	s := newServer(db, cfg["CF_ACCESS_TEAM_DOMAIN"], cfg["CF_ACCESS_AUD"], cfg["PUBLIC_ORIGIN"], cfg["ADMIN_EMAIL"], cfg["SESSION_SECRET"], envOr("WEB_DIR", "/web"))
	s.sitekey = os.Getenv("TURNSTILE_SITEKEY")
	s.contact = os.Getenv("CONTACT_EMAIL")
	if v := os.Getenv("CODES_PER_DAY"); v != "" {
		if s.codesPerDay, err = strconv.Atoi(v); err != nil || s.codesPerDay < 1 {
			log.Fatalf("CODES_PER_DAY must be a positive integer")
		}
	}
	if v := os.Getenv("POLL_DELETE_AFTER"); v != "" {
		if s.closeAt, err = time.Parse(time.RFC3339, v); err != nil {
			log.Fatalf("POLL_DELETE_AFTER: %v", err)
		}
		s.prune() // enforce at startup too, not only on the ticker
	}
	if sec := secretEnv("TURNSTILE_SECRET"); sec != "" {
		s.captcha = &turnstile{secret: sec, verifyURL: "https://challenges.cloudflare.com/turnstile/v0/siteverify",
			hostname: hostOf(s.origin), client: &http.Client{Timeout: 10 * time.Second}}
	} else {
		log.Print("TURNSTILE_SECRET not set: code requests will be rejected")
	}
	if m := (&smtpMailer{host: os.Getenv("SMTP_HOST"), port: os.Getenv("SMTP_PORT"), user: os.Getenv("SMTP_USER"),
		pass: secretEnv("SMTP_PASS"), from: os.Getenv("SMTP_FROM")}); m.host != "" && m.port != "" && m.from != "" && (m.user == "" || m.pass != "") {
		s.mail = m
	} else {
		log.Print("SMTP not configured: /api/otp/request answers 503 email_unavailable")
	}
	if err := s.auth.refresh(); err != nil {
		log.Printf("initial Access certs fetch failed (will retry on demand): %v", err)
	}
	go func() {
		for range time.Tick(10 * time.Minute) {
			s.prune()
		}
	}()
	go func() {
		for range time.Tick(time.Hour) {
			if err := s.auth.refresh(); err != nil {
				log.Printf("certs refresh: %v", err)
			}
		}
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Printf("listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func newServer(db *sql.DB, team, aud, origin, admin, secret, webDir string) *server {
	return &server{
		db:          db,
		auth:        newVerifier(team, aud),
		secret:      []byte(secret),
		webDir:      webDir,
		origin:      strings.TrimRight(origin, "/"),
		admin:       strings.ToLower(strings.TrimSpace(admin)),
		now:         time.Now,
		codesPerDay: 500,
		hostLimit:   newLimiter(120, time.Minute),
		apiLimit:    newLimiter(30, time.Minute),
		otpIPLimit:  newLimiter(10, time.Hour),
		voteLimit:   newLimiter(5, time.Hour),
	}
}

// secretEnv reads k from the file named by k_FILE (Docker secrets), falling back to the plain variable k.
func secretEnv(k string) string {
	if p := os.Getenv(k + "_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatalf("%s_FILE: %v", k, err)
		}
		return strings.TrimSpace(string(b))
	}
	return strings.TrimSpace(os.Getenv(k))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func hostOf(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one connection: SQLite serialises writers anyway
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS votes (
		id         INTEGER PRIMARY KEY,
		email      TEXT NOT NULL UNIQUE,
		favourite  TEXT NOT NULL,
		reactions  TEXT NOT NULL,
		comment    TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS otp_codes (
		email_key  TEXT PRIMARY KEY,
		code_mac   TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		attempts   INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS otp_sends (
		email_key TEXT NOT NULL,
		sent_at   INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS otp_sends_key ON otp_sends (email_key, sent_at);
	CREATE TABLE IF NOT EXISTS otp_burns (
		email_key TEXT NOT NULL,
		burnt_at  INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS otp_lockouts (
		email_key TEXT PRIMARY KEY,
		until     INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		id_hash    TEXT PRIMARY KEY,
		email      TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	);`)
	if err != nil {
		return nil, err
	}
	// Tighten files that existed before the umask was in place.
	_ = os.Chmod(filepath.Dir(path), 0o700)
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return db, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		var deleteAfter any // null when no deadline is set
		if !s.closeAt.IsZero() {
			deleteAfter = s.closeAt.UTC().Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, map[string]any{"turnstile_sitekey": s.sitekey, "contact_email": s.contact,
			"delete_after": deleteAfter, "processors": []string{"cloudflare", "resend"}})
	})
	mux.HandleFunc("GET /api/options", s.handleOptions)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/otp/request", s.csrf(s.handleOTPRequest))
	mux.HandleFunc("POST /api/otp/verify", s.csrf(s.handleOTPVerify))
	mux.HandleFunc("POST /api/logout", s.csrf(s.handleLogout))
	mux.HandleFunc("POST /api/vote", s.csrf(s.handleVote))
	mux.HandleFunc("GET /api/results", s.adminOnly(s.handleResults))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusNotFound, errBody("not_found")) })
	mux.HandleFunc("GET /pool", func(w http.ResponseWriter, r *http.Request) { s.serveFile(w, r, "pool.html") })
	mux.HandleFunc("GET /pool/results", s.adminOnly(func(w http.ResponseWriter, r *http.Request) { s.serveFile(w, r, "results.html") }))
	mux.HandleFunc("GET /results.html", http.NotFound) // only via /pool/results, which Access guards
	mux.HandleFunc("GET /results", http.NotFound)
	mux.Handle("GET /", s.static())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range securityHeaders {
			w.Header().Set(k, v)
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		ip := clientIP(r)
		if !s.hostLimit.allow(ip) || (strings.HasPrefix(r.URL.Path, "/api/") && !s.apiLimit.allow(ip)) {
			writeJSON(w, http.StatusTooManyRequests, errBody("rate_limited"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// csrf requires a JSON body and an Origin equal to the public origin.
func (s *server) csrf(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeJSON(w, http.StatusBadRequest, errBody("invalid"))
			return
		}
		if r.Header.Get("Origin") != s.origin {
			writeJSON(w, http.StatusForbidden, errBody("forbidden"))
			return
		}
		next(w, r)
	}
}

// adminOnly verifies the Cloudflare Access JWT (never the email header alone) and the admin email.
func (s *server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, err := s.auth.verify(tokenFrom(r))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
			return
		}
		if email != s.admin {
			writeJSON(w, http.StatusForbidden, errBody("forbidden"))
			return
		}
		next(w, r)
	}
}

// clientIP returns the rate-limit key: the IPv4 address, or the /64 of an IPv6 address.
func clientIP(r *http.Request) string {
	ip := r.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if addr = addr.Unmap(); addr.Is6() {
		p, _ := addr.Prefix(64)
		return p.String()
	}
	return addr.String()
}

func (s *server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join(s.webDir, name))
}

// static serves web/ (including / -> index.html) without directory listings.
func (s *server) static() http.Handler {
	fs := http.FileServer(http.Dir(s.webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(s.webDir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			if _, err := os.Stat(filepath.Join(p, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(code string) map[string]string { return map[string]string{"error": code} }

// decodeJSON decodes exactly one JSON object with no unknown fields, answering 400 otherwise.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		writeJSON(w, http.StatusBadRequest, errBody("invalid"))
		return false
	}
	return true
}

// loadOptions reads web/options.json on each call so content updates need no restart.
func (s *server) loadOptions() ([]json.RawMessage, map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(s.webDir, "options.json"))
	if err != nil {
		return nil, nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, err
	}
	ids := map[string]bool{}
	for _, o := range raw {
		var x struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(o, &x) == nil && x.ID != "" {
			ids[x.ID] = true
		}
	}
	return raw, ids, nil
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	jwtEmail, err := s.auth.verify(tokenFrom(r))
	isAdmin := err == nil && jwtEmail == s.admin
	email := s.sessionEmail(r)
	if email == "" {
		writeJSON(w, http.StatusOK, map[string]any{"email": nil, "voted": false, "is_admin": isAdmin})
		return
	}
	var voted bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM votes WHERE email = ?)`, email).Scan(&voted); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": email, "voted": voted, "is_admin": isAdmin})
}

func (s *server) handleOptions(w http.ResponseWriter, r *http.Request) {
	opts, _, err := s.loadOptions()
	if err != nil {
		log.Printf("options: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, errBody("unavailable"))
		return
	}
	rand.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	writeJSON(w, http.StatusOK, opts)
}

var reactionValues = map[string]bool{"love": true, "ok": true, "no": true}

func (s *server) handleVote(w http.ResponseWriter, r *http.Request) {
	if s.closed() {
		writeJSON(w, http.StatusGone, errBody("poll_closed"))
		return
	}
	email := s.sessionEmail(r)
	if email == "" {
		writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
		return
	}
	if !s.voteLimit.allow(email) {
		writeJSON(w, http.StatusTooManyRequests, errBody("rate_limited"))
		return
	}
	var v struct {
		Favourite string            `json:"favourite"`
		Reactions map[string]string `json:"reactions"`
		Comment   string            `json:"comment"`
	}
	if !decodeJSON(w, r, &v) {
		return
	}
	_, ids, err := s.loadOptions()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errBody("unavailable"))
		return
	}
	comment, ok := cleanComment(v.Comment)
	if !ok || !ids[v.Favourite] {
		writeJSON(w, http.StatusBadRequest, errBody("invalid"))
		return
	}
	for id, val := range v.Reactions {
		if !ids[id] || !reactionValues[val] {
			writeJSON(w, http.StatusBadRequest, errBody("invalid"))
			return
		}
	}
	if v.Reactions == nil {
		v.Reactions = map[string]string{}
	}
	reactions, _ := json.Marshal(v.Reactions)

	switch err := s.insertVote(email, v.Favourite, string(reactions), comment); {
	case errors.Is(err, errAlreadyVoted):
		writeJSON(w, http.StatusConflict, errBody("already_voted"))
	case err != nil:
		log.Printf("vote insert %s: %v", s.logID(email), err)
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
	default:
		writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	}
}

var errAlreadyVoted = errors.New("already voted")

func (s *server) insertVote(email, fav, reactions, comment string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM votes WHERE email = ?)`, email).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errAlreadyVoted
	}
	_, err = tx.Exec(`INSERT INTO votes (email, favourite, reactions, comment, created_at) VALUES (?, ?, ?, ?, ?)`,
		email, fav, reactions, comment, s.now().UTC().Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return errAlreadyVoted
		}
		return err
	}
	return tx.Commit()
}

// cleanComment strips control and format characters (keeping newlines and tabs) and enforces the length cap.
func cleanComment(c string) (string, bool) {
	if !utf8.ValidString(c) {
		return "", false
	}
	c = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !(unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return r
		}
		return -1
	}, c)
	c = strings.TrimSpace(c)
	return c, utf8.RuneCountInString(c) <= maxComment
}

func (s *server) handleResults(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT email, favourite, reactions, comment, created_at FROM votes ORDER BY created_at`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	defer rows.Close()
	type comment struct {
		Email   string `json:"email"`
		Comment string `json:"comment"`
		At      string `json:"at"`
	}
	favs := map[string]int{}
	reacts := map[string]map[string]int{}
	comments := []comment{}
	total := 0
	for rows.Next() {
		var email, fav, rj, c, at string
		if err := rows.Scan(&email, &fav, &rj, &c, &at); err != nil {
			writeJSON(w, http.StatusInternalServerError, errBody("internal"))
			return
		}
		total++
		favs[fav]++
		var m map[string]string
		_ = json.Unmarshal([]byte(rj), &m)
		for id, val := range m {
			if reacts[id] == nil {
				reacts[id] = map[string]int{"love": 0, "ok": 0, "no": 0}
			}
			reacts[id][val]++
		}
		if c != "" {
			comments = append(comments, comment{email, c, at})
		}
	}
	if rows.Err() != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("internal"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "favourites": favs, "reactions": reacts, "comments": comments})
}

// limiter is an in-memory per-key token bucket: burst tokens, refilled evenly over per.
type limiter struct {
	mu      sync.Mutex
	burst   float64
	rate    float64 // tokens per second
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// evict drops buckets that have refilled completely, so memory stays bounded. Called from the prune ticker.
func (l *limiter) evict() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

func newLimiter(burst int, per time.Duration) *limiter {
	return &limiter{burst: float64(burst), rate: float64(burst) / per.Seconds(), buckets: map[string]*bucket{}, now: time.Now}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
