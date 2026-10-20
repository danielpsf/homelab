package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Email one-time codes", func() {
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	Describe("requesting a code", func() {
		It("normalises the address and emails it a 6-digit code", func() {
			Expect(e.askCode(" Voter@Example.TEST ")).To(HaveHTTPStatus(http.StatusAccepted))
			var m sentMail
			Eventually(e.mail.sent).Should(Receive(&m))
			Expect(m.to).To(Equal("voter@example.test"))
			Expect(m.body).To(MatchRegexp(`\b\d{6}\b`))
		})

		It("answers the same way whether or not the email has voted", func() {
			c := e.login("voted@example.test")
			Expect(e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, c)).To(HaveHTTPStatus(http.StatusCreated))
			voted, fresh := e.askCode("voted@example.test"), e.askCode("fresh@example.test")
			Expect(voted).To(HaveHTTPStatus(http.StatusAccepted))
			Expect(fresh).To(HaveHTTPStatus(http.StatusAccepted))
			Expect(voted.Body.String()).To(Equal(fresh.Body.String()))
			Eventually(e.mail.sent).Should(Receive())
			Eventually(e.mail.sent).Should(Receive())
		})

		It("stores only HMACs of the email and the code", func() {
			code := e.requestCode("d@example.test")
			var key, stored string
			Expect(e.s.db.QueryRow(`SELECT email_key, code_mac FROM otp_codes`).Scan(&key, &stored)).To(Succeed())
			Expect(stored).To(Equal(e.s.mac("code", "d@example.test", code)))
			Expect(stored).NotTo(ContainSubstring(code))
			Expect(key).NotTo(ContainSubstring("@"))
			var plain int
			Expect(e.s.db.QueryRow(`SELECT COUNT(*) FROM otp_sends WHERE email_key LIKE '%@%'`).Scan(&plain)).To(Succeed())
			Expect(plain).To(BeZero())
		})

		DescribeTable("rejects an unusable address with 400",
			func(email string) {
				Expect(e.askCode(email)).To(HaveHTTPStatus(http.StatusBadRequest))
			},
			Entry("not an address", "not an email"),
			Entry("longer than 254 characters", strings.Repeat("a", 250)+"@x.io"),
		)

		DescribeTable("normalises and validates addresses",
			func(in, want string) {
				Expect(normaliseEmail(in)).To(Equal(want))
			},
			Entry("trims and lowercases", " A@B.io ", "a@b.io"),
			Entry("needs a dot in the domain", "a@b", ""),
			Entry("refuses spaces", "a b@c.io", ""),
			Entry("refuses empty labels", "a@b..io", ""),
			Entry("refuses a leading hyphen", "a@-b.io", ""),
			Entry("refuses CR and LF", "x\r\n@b.io", ""),
		)

		Context("when Turnstile does not pass", func() {
			DescribeTable("answers 400 turnstile_failed and issues no code",
				func(token string, mutate func(*env)) {
					if mutate != nil {
						mutate(e)
					}
					rec := e.do("POST", "/api/otp/request", `{"email":"t@example.test","turnstile":"`+token+`"}`, jsonPost)
					Expect(rec).To(HaveHTTPStatus(http.StatusBadRequest))
					Expect(rec.Body.String()).To(MatchJSON(`{"error":"turnstile_failed"}`))
					Consistently(e.mail.sent, 100*time.Millisecond).ShouldNot(Receive())
					Expect(e.count("otp_codes")).To(BeZero())
				},
				Entry("a token siteverify rejects", "bad", nil),
				Entry("an empty token", "", nil),
				Entry("a token solved for another hostname", "good", func(e *env) { e.s.captcha.hostname = "other.example" }),
			)
		})

		It("answers 503 email_unavailable when SMTP is not configured", func() {
			e.s.mail = nil
			rec := e.askCode("t@example.test")
			Expect(rec).To(HaveHTTPStatus(http.StatusServiceUnavailable))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"email_unavailable"}`))
		})

		Context("limits", func() {
			It("allows 3 codes per email per hour, and the limit survives a restart", func() {
				for i := 0; i < 3; i++ {
					e.requestCode("lim@example.test")
				}
				Expect(e.askCode("lim@example.test")).To(HaveHTTPStatus(http.StatusTooManyRequests))

				restarted := newServer(e.s.db, e.s.auth.issuer, testAud, testOrigin, testAdmin, testSecret, e.s.webDir)
				restarted.captcha, restarted.mail, restarted.now = e.s.captcha, e.s.mail, e.s.now
				e.h = restarted.routes()
				Expect(e.askCode("lim@example.test")).To(HaveHTTPStatus(http.StatusTooManyRequests))
			})

			It("allows 5 codes per email per 24 h", func() {
				for i := 0; i < 3; i++ {
					e.requestCode("day@example.test")
				}
				e.now = e.now.Add(61 * time.Minute)
				e.requestCode("day@example.test")
				e.requestCode("day@example.test")
				e.now = e.now.Add(61 * time.Minute)
				Expect(e.askCode("day@example.test")).To(HaveHTTPStatus(http.StatusTooManyRequests))
			})

			It("allows 10 code requests per IP per hour", func() {
				ip := map[string]string{"Content-Type": "application/json", "Origin": testOrigin, "CF-Connecting-IP": "198.51.100.1"}
				for i := 0; i < 10; i++ {
					Expect(e.askCode(fmt.Sprintf("ip%d@example.test", i), ip)).To(HaveHTTPStatus(http.StatusAccepted))
					Eventually(e.mail.sent).Should(Receive())
				}
				Expect(e.askCode("ipz@example.test", ip)).To(HaveHTTPStatus(http.StatusTooManyRequests))
			})

			It("stops at the global daily cap", func() {
				e.s.codesPerDay = 10
				for i := 0; i < e.s.codesPerDay; i++ {
					_, err := e.s.db.Exec(`INSERT INTO otp_sends (email_key, sent_at) VALUES (?, ?)`, "k", e.now.Unix())
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(e.askCode("x@example.test")).To(HaveHTTPStatus(http.StatusTooManyRequests))
			})
		})
	})

	Describe("verifying a code", func() {
		It("sets a __Host- session cookie that is HttpOnly, Secure, SameSite=Strict and lasts 24 h", func() {
			rec := e.verify("voter@example.test", e.requestCode("voter@example.test"))
			Expect(rec).To(HaveHTTPStatus(http.StatusOK))
			raw := rec.Header().Get("Set-Cookie")
			Expect(raw).To(SatisfyAll(
				HavePrefix(sessionCookie+"="), ContainSubstring("Path=/"), ContainSubstring("HttpOnly"),
				ContainSubstring("Secure"), ContainSubstring("SameSite=Strict"), ContainSubstring("Max-Age=86400"),
				Not(ContainSubstring("Domain=")),
			))
			c := rec.Result().Cookies()[0]
			Expect(c.Value).To(MatchRegexp(`^[0-9a-f]{64}$`))

			var plain int
			Expect(e.s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, c.Value).Scan(&plain)).To(Succeed())
			Expect(plain).To(BeZero(), "session id stored in plain")

			Expect(e.me(nil, c)).To(SatisfyAll(
				HaveKeyWithValue("email", "voter@example.test"),
				HaveKeyWithValue("voted", false),
				HaveKeyWithValue("is_admin", false),
			))
			e.now = e.now.Add(sessionTTL + time.Second)
			Expect(e.me(nil, c)).To(HaveKeyWithValue("email", BeNil()))
		})

		It("accepts a code only once", func() {
			code := e.requestCode("a@example.test")
			Expect(e.verify("a@example.test", code)).To(HaveHTTPStatus(http.StatusOK))
			rec := e.verify("a@example.test", code)
			Expect(rec).To(HaveHTTPStatus(http.StatusBadRequest))
			Expect(rec.Body.String()).To(MatchJSON(`{"error":"invalid_code"}`))
		})

		It("rejects a code after 10 minutes", func() {
			code := e.requestCode("b@example.test")
			e.now = e.now.Add(codeTTL + time.Second)
			Expect(e.verify("b@example.test", code)).To(HaveHTTPStatus(http.StatusBadRequest))
		})

		It("replaces the previous code when a new one is requested", func() {
			old := e.requestCode("c@example.test")
			fresh := e.requestCode("c@example.test")
			if old != fresh {
				Expect(e.verify("c@example.test", old)).To(HaveHTTPStatus(http.StatusBadRequest))
			}
			Expect(e.verify("c@example.test", fresh)).To(HaveHTTPStatus(http.StatusOK))
		})

		It("burns the code after 5 wrong attempts", func() {
			code := e.requestCode("d@example.test")
			wrong := "000000"
			if code == wrong {
				wrong = "111111"
			}
			for i := 0; i < maxCodeAttempts; i++ {
				Expect(e.verify("d@example.test", wrong)).To(HaveHTTPStatus(http.StatusBadRequest))
			}
			Expect(e.verify("d@example.test", code)).To(HaveHTTPStatus(http.StatusBadRequest))
		})

		Context("after 3 burnt codes", func() {
			var last string

			BeforeEach(func() {
				e.s.otpIPLimit = newLimiter(1000, time.Hour)
				for i := 0; i < burnsToLockout; i++ {
					last = e.requestCode("lock@example.test")
					for j := 0; j < maxCodeAttempts; j++ {
						e.verify("lock@example.test", "x")
					}
				}
				e.now = e.now.Add(61 * time.Minute) // past the hourly limit, inside the lockout
			})

			It("refuses new codes and verification for 24 h", func() {
				Expect(e.askCode("lock@example.test")).To(HaveHTTPStatus(http.StatusTooManyRequests))
				Expect(e.verify("lock@example.test", last)).To(HaveHTTPStatus(http.StatusBadRequest))
			})

			It("lets the email in again once the lockout ends", func() {
				e.now = e.now.Add(lockoutFor)
				e.s.prune()
				Expect(e.verify("lock@example.test", e.requestCode("lock@example.test"))).To(HaveHTTPStatus(http.StatusOK))
			})
		})
	})

	Describe("the code email", func() {
		DescribeTable("normalises the requested locale",
			func(in, want string) {
				Expect(normaliseLocale(in)).To(Equal(want))
			},
			EntryDescription("%q becomes %s"),
			Entry(nil, "pt-BR", "pt-BR"), Entry(nil, "pt-PT", "pt-BR"), Entry(nil, "pt", "pt-BR"), Entry(nil, "PT_br", "pt-BR"),
			Entry(nil, "en-GB", "en-US"), Entry(nil, "en", "en-US"),
			Entry(nil, "zh-TW", "zh-CN"), Entry(nil, "zh-Hans-CN", "zh-CN"), Entry(nil, "ZH-cn", "zh-CN"),
			Entry(nil, "es-MX", "es"),
			Entry(nil, "de", "de"), Entry(nil, "de-AT", "de"), Entry(nil, "de_CH", "de"),
			Entry(nil, "ar", "ar"), Entry(nil, "ar-EG", "ar"),
			Entry(nil, "tr", "tr"), Entry(nil, "tr-TR", "tr"),
			Entry(nil, "hi", "hi"), Entry(nil, "hi-IN", "hi"),
			Entry(nil, "ja", "ja"), Entry(nil, "ja-JP", "ja"),
			Entry(nil, "he", "he"), Entry(nil, "he-IL", "he"), Entry(nil, "iw", "he"), Entry(nil, "iw-IL", "he"),
			Entry(nil, "fr-FR", "en-US"), Entry(nil, "", "en-US"), Entry(nil, "xx", "en-US"), Entry(nil, "-", "en-US"),
		)

		It("uses the built-in English text when web/i18n is missing", func() {
			Expect(loadEmailTexts(GinkgoT().TempDir())).To(HaveKeyWithValue("ja", defaultEmail))
		})

		Context("with web/i18n files", func() {
			var s *server

			BeforeEach(func() {
				dir := GinkgoT().TempDir()
				writeI18n(dir, map[string]string{
					"en-US": `{"email":{"subject":"Code for Tolon","body":"Code {code}, {minutes} min."}}`,
					"ja":    `{"email":{"subject":"Tolon 投票コード","body":"コードは {code} です。{minutes} 分で失効します。"}}`,
					"de":    `{"email":{"subject":"Dein Tolon-Code"}}`,
					"tr":    `{"email.subject":"Tolon kodunuz","email.body":"Kodunuz {code}."}`,
					"es":    `not json`,
				})
				s = &server{emails: loadEmailTexts(dir)}
			})

			DescribeTable("fills in {code} and {minutes}, falling back to en-US",
				func(locale, subject, body string) {
					subj, b := s.codeEmail(locale, "123456")
					Expect(subj).To(Equal(subject))
					Expect(b).To(Equal(body))
				},
				Entry("a complete file", "ja", "Tolon 投票コード", "コードは 123456 です。10 分で失効します。\n"),
				Entry("a missing body key", "de", "Dein Tolon-Code", "Code 123456, 10 min.\n"),
				Entry("tr-* mapped to tr", "tr-TR", "Tolon kodunuz", "Kodunuz 123456.\n"),
				Entry("flat email.subject / email.body keys", "tr", "Tolon kodunuz", "Kodunuz 123456.\n"),
				Entry("an unknown locale", "fr-FR", "Code for Tolon", "Code 123456, 10 min.\n"),
				Entry("an invalid file", "es-AR", "Code for Tolon", "Code 123456, 10 min.\n"),
				Entry("a missing file", "hi", "Code for Tolon", "Code 123456, 10 min.\n"),
			)
		})

		It("sends the localised text with a code that verifies", func() {
			writeI18n(e.s.webDir, map[string]string{"pt-BR": `{"email":{"subject":"Seu código","body":"Seu código é {code}. Expira em {minutes} minutos."}}`})
			e.s.emails = loadEmailTexts(e.s.webDir)
			rec := e.do("POST", "/api/otp/request", `{"email":"loc@example.test","turnstile":"good","locale":"pt-PT"}`, jsonPost)
			Expect(rec).To(HaveHTTPStatus(http.StatusAccepted))
			var m sentMail
			Eventually(e.mail.sent).Should(Receive(&m))
			code := codeRe.FindString(m.body)
			Expect(m.subject).To(Equal("Seu código"))
			Expect(m.body).To(Equal("Seu código é " + code + ". Expira em 10 minutos.\n"))
			Expect(e.verify("loc@example.test", code)).To(HaveHTTPStatus(http.StatusOK))
		})

		DescribeTable("is plain-text UTF-8 with an RFC 2047 subject that round-trips",
			func(subject string) {
				from, err := mail.ParseAddress("Tolon <noreply@example.test>")
				Expect(err).NotTo(HaveOccurred())
				msg, err := mail.ReadMessage(bytes.NewReader(buildMessage(from, "v@example.test", subject, "コード 123456\nline two")))
				Expect(err).NotTo(HaveOccurred())
				enc := msg.Header.Get("Subject")
				Expect(enc).To(MatchRegexp(`^[\x20-\x7e]+$`), "subject header must be ASCII")
				Expect(new(mime.WordDecoder).DecodeHeader(enc)).To(Equal(subject))
				Expect(msg.Header.Get("Content-Type")).To(Equal("text/plain; charset=UTF-8"))
				body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(body)).To(Equal("コード 123456\r\nline two"))
			},
			Entry("English", "Your Tolon poll code"),
			Entry("Arabic", "رمز تصويت Tolon"),
			Entry("Hindi", "Tolon मतदान कोड"),
			Entry("Chinese", "Tolon 投票验证码"),
			Entry("Japanese", "Tolon 投票コード"),
			Entry("Hebrew", "קוד ההצבעה של Tolon"),
		)
	})

	DescribeTable("describes SMTP failures without the address",
		func(err error, want string) {
			got := smtpErrClass(err)
			Expect(got).To(Equal(want))
			Expect(got).NotTo(ContainSubstring("@"))
		},
		Entry("a reply that echoes the recipient",
			&textproto.Error{Code: 550, Msg: "5.1.1 <victim@example.test>: Recipient address rejected"}, "smtp reply 550 (class 5xx)"),
		Entry("a wrapped reply",
			fmt.Errorf("rcpt: %w", &textproto.Error{Code: 452, Msg: "too many recipients victim@example.test"}), "smtp reply 452 (class 4xx)"),
		Entry("a plain error with an address",
			errors.New("smtp: victim@example.test refused"), "error *errors.errorString"),
		Entry("a server without STARTTLS", errNoSTARTTLS, "no STARTTLS offered"),
	)
})
