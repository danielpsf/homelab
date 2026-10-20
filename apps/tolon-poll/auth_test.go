package main

import (
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Admin results behind Cloudflare Access", func() {
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	DescribeTable("verifies the Access JWT on /api/results and /pool/results",
		func(token func(*env) string, want int) {
			t := token(e)
			for _, p := range []string{"/api/results", "/pool/results"} {
				Expect(e.do("GET", p, "", e.jwt(t))).To(HaveHTTPStatus(want), p)
			}
		},
		Entry("accepts the admin, whatever the email case",
			func(e *env) string { return e.tok("Admin@Example.test", nil) }, http.StatusOK),
		Entry("accepts an aud claim given as a plain string",
			func(e *env) string { return e.tok(testAdmin, func(c map[string]any) { c["aud"] = testAud }) }, http.StatusOK),
		Entry("forbids a valid token for someone else",
			func(e *env) string { return e.tok("someone@example.test", nil) }, http.StatusForbidden),
		Entry("rejects a missing token",
			func(*env) string { return "" }, http.StatusUnauthorized),
		Entry("rejects a malformed token",
			func(*env) string { return "a.b.c" }, http.StatusUnauthorized),
		Entry("rejects a wrong audience",
			func(e *env) string { return e.tok(testAdmin, func(c map[string]any) { c["aud"] = "other" }) }, http.StatusUnauthorized),
		Entry("rejects a wrong issuer",
			func(e *env) string {
				return e.tok(testAdmin, func(c map[string]any) { c["iss"] = "https://evil.cloudflareaccess.com" })
			}, http.StatusUnauthorized),
		Entry("rejects an expired token",
			func(e *env) string {
				return e.tok(testAdmin, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })
			}, http.StatusUnauthorized),
		Entry("rejects a token without exp",
			func(e *env) string { return e.tok(testAdmin, func(c map[string]any) { delete(c, "exp") }) }, http.StatusUnauthorized),
		Entry("rejects a token that is not valid yet",
			func(e *env) string {
				return e.tok(testAdmin, func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() })
			}, http.StatusUnauthorized),
		Entry("rejects a signature from another key",
			func(e *env) string {
				return sign(otherKey, "k1", map[string]any{"aud": testAud, "iss": e.s.auth.issuer, "email": testAdmin, "exp": time.Now().Unix() + 60})
			}, http.StatusUnauthorized),
		Entry("rejects an unknown key id",
			func(e *env) string {
				return sign(testKey, "k9", map[string]any{"aud": testAud, "iss": e.s.auth.issuer, "email": testAdmin, "exp": time.Now().Unix() + 60})
			}, http.StatusUnauthorized),
		Entry("rejects alg none",
			func(*env) string {
				return b64([]byte(`{"alg":"none","kid":"k1"}`)) + "." + b64([]byte(`{"aud":"test-aud","email":"admin@example.test"}`)) + "."
			}, http.StatusUnauthorized),
	)

	It("never trusts the Cf-Access-Authenticated-User-Email header on its own", func() {
		Expect(e.do("GET", "/api/results", "", map[string]string{"Cf-Access-Authenticated-User-Email": testAdmin})).
			To(HaveHTTPStatus(http.StatusUnauthorized))
	})

	It("reads the token from the CF_Authorization cookie too", func() {
		Expect(e.do("GET", "/api/results", "", nil, &http.Cookie{Name: "CF_Authorization", Value: e.tok(testAdmin, nil)})).
			To(HaveHTTPStatus(http.StatusOK))
	})

	It("does not treat a voter session for the admin email as admin", func() {
		Expect(e.do("GET", "/api/results", "", nil, e.login(testAdmin))).To(HaveHTTPStatus(http.StatusUnauthorized))
	})

	DescribeTable("serves the results page only at /pool/results",
		func(path string) {
			Expect(e.do("GET", path, "", nil)).To(HaveHTTPStatus(http.StatusNotFound))
		},
		Entry("not at /results", "/results"),
		Entry("not at /results.html", "/results.html"),
	)

	Context("in /api/me", func() {
		It("reports is_admin only with a valid admin token", func() {
			Expect(e.me(e.jwt(e.tok(testAdmin, nil)))).To(HaveKeyWithValue("is_admin", true))
			Expect(e.me(e.jwt(e.tok("x@example.test", nil)))).To(HaveKeyWithValue("is_admin", false))
			Expect(e.me(nil)).To(HaveKeyWithValue("is_admin", false))
		})
	})

	It("returns totals, reactions and the comments with email and time", func() {
		_, err := e.s.db.Exec(`INSERT INTO votes (email, favourite, reactions, comment, created_at)
			VALUES ('v@x.test','lon','{"tol":"no"}','hi','2026-01-01T00:00:00Z')`)
		Expect(err).NotTo(HaveOccurred())
		rec := e.do("GET", "/api/results", "", e.jwt(e.tok(testAdmin, nil)))
		Expect(rec).To(HaveHTTPStatus(http.StatusOK))
		Expect(rec.Body.String()).To(MatchJSON(`{
			"total": 1,
			"favourites": {"lon": 1},
			"reactions": {"tol": {"love": 0, "ok": 0, "no": 1}},
			"comments": [{"email": "v@x.test", "comment": "hi", "at": "2026-01-01T00:00:00Z"}]
		}`))
	})

	Context("when many requests carry an unknown key id at once", func() {
		var token string

		BeforeEach(func() {
			later := time.Now().Add(time.Minute) // past the 30 s refetch guard
			e.s.auth.now = func() time.Time { return later }
			token = sign(testKey, "rotated", map[string]any{"aud": testAud, "iss": e.s.auth.issuer, "email": testAdmin, "exp": later.Unix() + 60})
		})

		It("fetches the certs only once", func() {
			before := e.certHits.Load()
			var wg sync.WaitGroup
			for i := 0; i < 50; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					_, _ = e.s.auth.verify(token)
				}()
			}
			wg.Wait()
			Expect(e.certHits.Load() - before).To(BeEquivalentTo(1))
		})
	})
})
