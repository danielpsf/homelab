package main

import (
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Voting", func() {
	const body = `{"favourite":"lon","reactions":{"lon":"love","tol":"no"},"comment":"nice\u0007 one"}`
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	Context("without a session", func() {
		It("answers 401", func() {
			Expect(e.do("POST", "/api/vote", body, jsonPost)).To(HaveHTTPStatus(http.StatusUnauthorized))
		})

		It("answers 401 for a forged session cookie", func() {
			forged := &http.Cookie{Name: sessionCookie, Value: strings.Repeat("a", 64)}
			Expect(e.do("POST", "/api/vote", body, jsonPost, forged)).To(HaveHTTPStatus(http.StatusUnauthorized))
		})
	})

	Context("with a session", func() {
		var session *http.Cookie

		BeforeEach(func() {
			session = e.login("voter@example.test")
		})

		Context("after the first vote", func() {
			JustBeforeEach(func() {
				rec := e.do("POST", "/api/vote", body, jsonPost, session)
				Expect(rec).To(HaveHTTPStatus(http.StatusCreated))
				Expect(rec.Body.String()).To(MatchJSON(`{"ok":true}`))
			})

			It("answers 409 already_voted to the same email in another session", func() {
				other := e.login("VOTER@example.test")
				rec := e.do("POST", "/api/vote", body, jsonPost, other)
				Expect(rec).To(HaveHTTPStatus(http.StatusConflict))
				Expect(rec.Body.String()).To(MatchJSON(`{"error":"already_voted"}`))
			})

			It("shows voted in /api/me", func() {
				Expect(e.me(nil, session)).To(HaveKeyWithValue("voted", true))
			})

			It("stores the comment without control characters", func() {
				var comment string
				Expect(e.s.db.QueryRow(`SELECT comment FROM votes`).Scan(&comment)).To(Succeed())
				Expect(comment).To(Equal("nice one"))
			})
		})

		It("ends the session on logout", func() {
			rec := e.do("POST", "/api/logout", `{}`, jsonPost, session)
			Expect(rec).To(HaveHTTPStatus(http.StatusOK))
			Expect(rec).To(HaveHTTPHeaderWithValue("Set-Cookie", ContainSubstring("Max-Age=0")))
			Expect(e.me(nil, session)).To(HaveKeyWithValue("email", BeNil()))
		})

		It("accepts a comment of exactly 1000 characters", func() {
			ok := `{"favourite":"tol","comment":"` + strings.Repeat("é", 1000) + `"}`
			Expect(e.do("POST", "/api/vote", ok, jsonPost, session)).To(HaveHTTPStatus(http.StatusCreated))
		})

		DescribeTable("rejects an invalid body with 400 invalid",
			func(payload string) {
				e.s.voteLimit = newLimiter(100, time.Hour)
				rec := e.do("POST", "/api/vote", payload, jsonPost, session)
				Expect(rec).To(HaveHTTPStatus(http.StatusBadRequest))
				Expect(rec.Body.String()).To(MatchJSON(`{"error":"invalid"}`))
			},
			Entry("no favourite", `{"reactions":{}}`),
			Entry("an unknown favourite", `{"favourite":"nope"}`),
			Entry("a reaction for an unknown option", `{"favourite":"lon","reactions":{"zzz":"love"}}`),
			Entry("an unknown reaction value", `{"favourite":"lon","reactions":{"lon":"meh"}}`),
			Entry("a comment over 1000 characters", `{"favourite":"lon","comment":"`+strings.Repeat("a", 1001)+`"}`),
			Entry("an unknown field", `{"favourite":"lon","ip":"1.2.3.4"}`),
			Entry("a body that is not JSON", `favourite=lon`),
			Entry("trailing data", `{"favourite":"lon"}{"favourite":"tol"}`),
			Entry("a body over 16 KB", `{"favourite":"lon","comment":"`+strings.Repeat("a", 17<<10)+`"}`),
		)

		It("allows 5 vote attempts per email per hour", func() {
			for i := 0; i < 5; i++ {
				Expect(e.do("POST", "/api/vote", `{}`, jsonPost, session)).To(HaveHTTPStatus(http.StatusBadRequest))
			}
			rec := e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, session)
			Expect(rec).To(HaveHTTPStatus(http.StatusTooManyRequests))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"rate_limited"}`))
		})

		DescribeTable("refuses cross-site POSTs (CSRF) on every state-changing endpoint",
			func(hdr map[string]string) {
				for _, p := range []string{"/api/vote", "/api/otp/request", "/api/otp/verify", "/api/logout"} {
					Expect(e.do("POST", p, `{"favourite":"lon"}`, hdr, session).Code).To(BeElementOf(http.StatusBadRequest, http.StatusForbidden), p)
				}
				Expect(e.count("votes")).To(BeZero())
			},
			Entry("no Origin", map[string]string{"Content-Type": "application/json"}),
			Entry("another Origin", map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}),
			Entry("the poll over http", map[string]string{"Content-Type": "application/json", "Origin": "http://tolon.example.test"}),
			Entry("a form body", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": testOrigin}),
			Entry("a text/plain body", map[string]string{"Content-Type": "text/plain", "Origin": testOrigin}),
		)

		It("accepts JSON with a charset parameter from the poll's Origin", func() {
			hdr := map[string]string{"Content-Type": "application/json; charset=utf-8", "Origin": testOrigin}
			Expect(e.do("POST", "/api/vote", `{"favourite":"lon"}`, hdr, session)).To(HaveHTTPStatus(http.StatusCreated))
		})
	})
})
