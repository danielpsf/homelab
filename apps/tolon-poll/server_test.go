package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP server", func() {
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	Describe("public routes", func() {
		It("serves the landing page at /", func() {
			Expect(e.do("GET", "/", "", nil)).To(SatisfyAll(HaveHTTPStatus(http.StatusOK), HaveHTTPBody(ContainSubstring("landing"))))
		})

		It("serves the poll at /pool", func() {
			Expect(e.do("GET", "/pool", "", nil)).To(SatisfyAll(HaveHTTPStatus(http.StatusOK), HaveHTTPBody(ContainSubstring("pool"))))
		})

		It("does not list directories", func() {
			Expect(e.do("GET", "/assets/", "", nil)).To(HaveHTTPStatus(http.StatusNotFound))
		})

		It("answers an anonymous /api/me with a null email", func() {
			Expect(e.me(nil)).To(SatisfyAll(HaveKeyWithValue("email", BeNil()), HaveKeyWithValue("voted", false)))
		})

		It("returns every option from /api/options", func() {
			rec := e.do("GET", "/api/options", "", nil)
			Expect(rec).To(HaveHTTPStatus(http.StatusOK))
			var opts []map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &opts)).To(Succeed())
			Expect(opts).To(ConsistOf(HaveKeyWithValue("id", "lon"), HaveKeyWithValue("id", "tol")))
		})

		It("answers /healthz", func() {
			Expect(e.do("GET", "/healthz", "", nil)).To(HaveHTTPStatus(http.StatusOK))
		})

		It("sets the security headers", func() {
			rec := e.do("GET", "/api/options", "", nil)
			Expect(rec).To(HaveHTTPHeaderWithValue("Content-Security-Policy", SatisfyAll(
				ContainSubstring("default-src 'self'"),
				ContainSubstring("script-src 'self' https://challenges.cloudflare.com"),
				ContainSubstring("frame-src https://challenges.cloudflare.com"),
				ContainSubstring("connect-src 'self'"),
				ContainSubstring("frame-ancestors 'none'"),
				ContainSubstring("base-uri 'none'"),
				ContainSubstring("object-src 'none'"),
			)))
			Expect(rec).To(HaveHTTPHeaderWithValue("X-Content-Type-Options", "nosniff"))
			Expect(rec).To(HaveHTTPHeaderWithValue("Strict-Transport-Security", "max-age=31536000"))
			Expect(rec).To(HaveHTTPHeaderWithValue("Referrer-Policy", "no-referrer"))
			Expect(rec).To(HaveHTTPHeaderWithValue("Permissions-Policy", ContainSubstring("camera=()")))
		})
	})

	Describe("GET /api/config", func() {
		It("serves the sitekey and a null delete_after when no deadline is set", func() {
			rec := e.do("GET", "/api/config", "", nil)
			Expect(rec).To(HaveHTTPStatus(http.StatusOK))
			Expect(rec.Body.String()).To(MatchJSON(`{"turnstile_sitekey":"site-key","contact_email":"","delete_after":null,"processors":["cloudflare","resend"]}`))
		})

		It("serves the contact address and the deadline for the privacy note", func() {
			e.s.contact = "poll@example.test"
			e.s.closeAt = time.Date(2027, 1, 18, 23, 59, 59, 0, time.UTC)
			Expect(e.do("GET", "/api/config", "", nil).Body.String()).To(MatchJSON(
				`{"turnstile_sitekey":"site-key","contact_email":"poll@example.test","delete_after":"2027-01-18T23:59:59Z","processors":["cloudflare","resend"]}`))
		})
	})

	Describe("the retention deadline (POLL_DELETE_AFTER)", func() {
		var session *http.Cookie

		BeforeEach(func() {
			session = e.login("keep@example.test")
			Expect(e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, session)).To(HaveHTTPStatus(http.StatusCreated))
			e.requestCode("pending@example.test")
			e.s.closeAt = e.now.Add(time.Hour)
		})

		total := func() (n int) {
			for _, t := range []string{"votes", "sessions", "otp_codes", "otp_sends", "otp_burns", "otp_lockouts"} {
				n += e.count(t)
			}
			return n
		}

		It("keeps the data before the deadline", func() {
			e.s.prune()
			Expect(total()).NotTo(BeZero())
		})

		Context("once the deadline has passed", func() {
			JustBeforeEach(func() {
				e.now = e.now.Add(time.Hour + time.Second)
				e.s.prune()
			})

			It("deletes every vote, session, code and limit row", func() {
				Expect(total()).To(BeZero())
			})

			It("closes voting and code requests with 410", func() {
				Expect(e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, session)).To(HaveHTTPStatus(http.StatusGone))
				Expect(e.askCode("x@example.test")).To(HaveHTTPStatus(http.StatusGone))
			})
		})
	})

	Describe("rate limits", func() {
		DescribeTable("keys clients by IPv4 address or IPv6 /64",
			func(in, want string) {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("CF-Connecting-IP", in)
				Expect(clientIP(r)).To(Equal(want))
			},
			Entry("IPv4", "203.0.113.7", "203.0.113.7"),
			Entry("IPv4-mapped IPv6", "::ffff:203.0.113.7", "203.0.113.7"),
			Entry("IPv6", "2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"),
			Entry("IPv6 in the same /64", "2001:db8:1:2:ffff:ffff::99", "2001:db8:1:2::/64"),
			Entry("IPv6 in the next /64", "2001:db8:1:3::1", "2001:db8:1:3::/64"),
		)

		It("allows 30 API requests per minute per IP", func() {
			ip := map[string]string{"CF-Connecting-IP": "203.0.113.7"}
			for i := 0; i < 30; i++ {
				Expect(e.do("GET", "/api/options", "", ip)).To(HaveHTTPStatus(http.StatusOK))
			}
			Expect(e.do("GET", "/api/options", "", ip)).To(HaveHTTPStatus(http.StatusTooManyRequests))
			Expect(e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "203.0.113.8"})).To(HaveHTTPStatus(http.StatusOK))
		})

		It("counts a whole IPv6 /64 as one client", func() {
			for i := 0; i < 30; i++ {
				e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "2001:db8:5:5::" + strconv.Itoa(i+1)})
			}
			Expect(e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "2001:db8:5:5:ffff::1"})).
				To(HaveHTTPStatus(http.StatusTooManyRequests))
		})

		It("allows 120 requests per minute per IP on the whole host", func() {
			host := map[string]string{"CF-Connecting-IP": "203.0.113.9"}
			for i := 0; i < 120; i++ {
				e.do("GET", "/pool", "", host)
			}
			Expect(e.do("GET", "/pool", "", host)).To(HaveHTTPStatus(http.StatusTooManyRequests))
		})
	})

	Describe("the token bucket", func() {
		var (
			l   *limiter
			now time.Time
		)

		BeforeEach(func() {
			l = newLimiter(1, time.Minute)
			now = time.Now()
			l.now = func() time.Time { return now }
		})

		It("refills over its period", func() {
			Expect(l.allow("k")).To(BeTrue())
			Expect(l.allow("k")).To(BeFalse())
			now = now.Add(61 * time.Second)
			Expect(l.allow("k")).To(BeTrue())
		})

		It("evicts only buckets that have refilled completely", func() {
			l.allow("k")
			l.allow("other")
			now = now.Add(30 * time.Second) // both half refilled
			l.evict()
			Expect(l.buckets).To(HaveKey("k"))
			now = now.Add(time.Minute)
			l.evict()
			Expect(l.buckets).To(BeEmpty())
		})
	})

	Describe("secrets", func() {
		BeforeEach(func() {
			DeferCleanup(os.Unsetenv, "TP_SECRET")
			DeferCleanup(os.Unsetenv, "TP_SECRET_FILE")
			Expect(os.Setenv("TP_SECRET", "from-env")).To(Succeed())
		})

		It("falls back to the plain variable", func() {
			Expect(secretEnv("TP_SECRET")).To(Equal("from-env"))
		})

		It("prefers the file named by the _FILE variable", func() {
			f := filepath.Join(GinkgoT().TempDir(), "s")
			Expect(os.WriteFile(f, []byte("from-file\n"), 0o600)).To(Succeed())
			Expect(os.Setenv("TP_SECRET_FILE", f)).To(Succeed())
			Expect(secretEnv("TP_SECRET")).To(Equal("from-file"))
		})
	})

	It("keeps the database directory and files owner-only", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "data")
		Expect(os.Mkdir(dir, 0o775)).To(Succeed())
		path := filepath.Join(dir, "poll.db")
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())
		db, err := openDB(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)

		mode := func(p string) os.FileMode {
			fi, err := os.Stat(p)
			Expect(err).NotTo(HaveOccurred())
			return fi.Mode().Perm()
		}
		Expect(mode(dir)).To(Equal(os.FileMode(0o700)))
		for _, f := range []string{path, path + "-wal", path + "-shm"} {
			if _, err := os.Stat(f); err == nil {
				Expect(mode(f)).To(Equal(os.FileMode(0o600)), filepath.Base(f))
			}
		}
	})
})
