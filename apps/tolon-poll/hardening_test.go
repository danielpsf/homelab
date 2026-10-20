package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Static file serving", func() {
	var e *env

	BeforeEach(func() {
		e = newEnv()
		// a file next to web/, which must never be reachable
		Expect(os.WriteFile(filepath.Join(filepath.Dir(e.s.webDir), "secret.txt"), []byte("TOP-SECRET"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(e.s.webDir, "assets", "ok.txt"), []byte("public"), 0o644)).To(Succeed())
	})

	It("serves files inside web/", func() {
		Expect(e.do("GET", "/assets/ok.txt", "", nil)).To(SatisfyAll(HaveHTTPStatus(http.StatusOK), HaveHTTPBody("public")))
	})

	DescribeTable("never serves a file outside web/",
		func(target string) {
			rec := e.do("GET", target, "", nil)
			Expect(rec.Code).NotTo(Equal(http.StatusOK))
			Expect(rec.Body.String()).NotTo(ContainSubstring("TOP-SECRET"))
		},
		Entry("dot-dot", "/../secret.txt"),
		Entry("dot-dot under a directory", "/assets/../../secret.txt"),
		Entry("encoded dots", "/%2e%2e/secret.txt"),
		Entry("encoded slash", "/..%2fsecret.txt"),
		Entry("encoded dots under a directory", "/assets/%2e%2e/%2e%2e/secret.txt"),
		Entry("backslashes", "/..%5csecret.txt"),
		Entry("a double slash", "//../secret.txt"),
	)
})

var _ = Describe("Access signing key rotation", func() {
	var (
		e     *env
		later time.Time
	)

	BeforeEach(func() {
		e = newEnv()
		later = time.Now()
		e.s.auth.now = func() time.Time { return later }
		e.certKeys.Store([]map[string]string{jwk("k2", otherKey)}) // Cloudflare rotates k1 -> k2
	})

	newToken := func() string {
		return sign(otherKey, "k2", map[string]any{"aud": testAud, "iss": e.s.auth.issuer, "email": testAdmin, "exp": later.Unix() + 60})
	}

	It("does not refetch within 30 s of the last fetch", func() {
		before := e.certHits.Load()
		_, err := e.s.auth.verify(newToken())
		Expect(err).To(HaveOccurred())
		Expect(e.certHits.Load()).To(Equal(before))
	})

	Context("once the refetch guard has passed", func() {
		JustBeforeEach(func() {
			later = later.Add(time.Minute)
		})

		It("accepts tokens signed with the new key", func() {
			Expect(e.s.auth.verify(newToken())).To(Equal(testAdmin))
		})

		It("stops accepting the retired key after the refresh", func() {
			_, err := e.s.auth.verify(newToken())
			Expect(err).NotTo(HaveOccurred())
			old := sign(testKey, "k1", map[string]any{"aud": testAud, "iss": e.s.auth.issuer, "email": testAdmin, "exp": later.Unix() + 60})
			_, err = e.s.auth.verify(old)
			Expect(err).To(MatchError(errUnauthorized))
		})
	})
})

var _ = Describe("prune", func() {
	var e *env

	BeforeEach(func() {
		e = newEnv()
		now := e.now.Unix()
		day := int64((24 * time.Hour).Seconds())
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO sessions (id_hash, email, expires_at) VALUES (?, ?, ?)`, []any{"live", "a@x.test", now + 60}},
			{`INSERT INTO sessions (id_hash, email, expires_at) VALUES (?, ?, ?)`, []any{"dead", "b@x.test", now - 1}},
			{`INSERT INTO otp_codes (email_key, code_mac, expires_at, attempts) VALUES (?, ?, ?, 0)`, []any{"live", "m", now + 60}},
			{`INSERT INTO otp_codes (email_key, code_mac, expires_at, attempts) VALUES (?, ?, ?, 0)`, []any{"dead", "m", now - 1}},
			{`INSERT INTO otp_sends (email_key, sent_at) VALUES (?, ?)`, []any{"live", now - 60}},
			{`INSERT INTO otp_sends (email_key, sent_at) VALUES (?, ?)`, []any{"dead", now - day - 1}},
			{`INSERT INTO otp_burns (email_key, burnt_at) VALUES (?, ?)`, []any{"live", now - 60}},
			{`INSERT INTO otp_burns (email_key, burnt_at) VALUES (?, ?)`, []any{"dead", now - day - 1}},
			{`INSERT INTO otp_lockouts (email_key, until) VALUES (?, ?)`, []any{"live", now + 60}},
			{`INSERT INTO otp_lockouts (email_key, until) VALUES (?, ?)`, []any{"dead", now - 1}},
			{`INSERT INTO votes (email, favourite, reactions, comment, created_at) VALUES ('v@x.test','lon','{}','','2026-01-01T00:00:00Z')`, nil},
		} {
			_, err := e.s.db.Exec(q.sql, q.args...)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	DescribeTable("drops only expired rows",
		func(table, key string) {
			e.s.prune()
			var keys []string
			rows, err := e.s.db.Query(`SELECT ` + key + ` FROM ` + table)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(rows.Close)
			for rows.Next() {
				var k string
				Expect(rows.Scan(&k)).To(Succeed())
				keys = append(keys, k)
			}
			Expect(keys).To(Equal([]string{"live"}))
		},
		Entry("sessions past their expiry", "sessions", "id_hash"),
		Entry("codes past their expiry", "otp_codes", "email_key"),
		Entry("sends older than 24 h", "otp_sends", "email_key"),
		Entry("burns older than 24 h", "otp_burns", "email_key"),
		Entry("lockouts that have ended", "otp_lockouts", "email_key"),
	)

	It("never touches votes before the deadline", func() {
		e.s.prune()
		Expect(e.count("votes")).To(Equal(1))
	})

	It("evicts refilled rate limit buckets", func() {
		e.do("GET", "/api/options", "", map[string]string{"CF-Connecting-IP": "203.0.113.50"})
		Expect(e.s.apiLimit.buckets).To(HaveKey("203.0.113.50"))
		for _, l := range []*limiter{e.s.hostLimit, e.s.apiLimit, e.s.otpIPLimit, e.s.voteLimit} {
			l.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		}
		e.s.prune()
		Expect(e.s.apiLimit.buckets).To(BeEmpty())
		Expect(e.s.hostLimit.buckets).To(BeEmpty())
	})
})

var _ = Describe("Logs", func() {
	var (
		e    *env
		logs *syncBuffer
	)

	BeforeEach(func() {
		e = newEnv()
		logs = &syncBuffer{}
		log.SetOutput(logs)
		DeferCleanup(log.SetOutput, GinkgoWriter)
	})

	It("never contain a full email address", func() {
		session := e.login("private.person@example.test")
		Expect(e.do("POST", "/api/vote", `{"favourite":"lon"}`, jsonPost, session)).To(HaveHTTPStatus(http.StatusCreated))

		e.mail.fail = &textproto.Error{Code: 550, Msg: "5.1.1 <private.person@example.test> rejected"}
		Expect(e.askCode("private.person@example.test")).To(HaveHTTPStatus(http.StatusAccepted))
		Eventually(logs.String).Should(ContainSubstring("otp mail"))

		out := logs.String()
		Expect(out).To(ContainSubstring(e.s.logID("private.person@example.test")))
		Expect(out).To(ContainSubstring("smtp reply 550"))
		Expect(out).NotTo(ContainSubstring("@example.test"))
		Expect(out).NotTo(ContainSubstring("private.person"))
	})
})

// fakeSMTP is a minimal SMTP server that records every command it receives.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	cmds []string
}

func startFakeSMTP(mode string) *fakeSMTP {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	f := &fakeSMTP{ln: ln}
	DeferCleanup(ln.Close)
	cert := selfSignedCert()
	go func() {
		defer GinkgoRecover()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, mode, cert)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn, mode string, cert tls.Certificate) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		f.mu.Lock()
		f.cmds = append(f.cmds, cmd)
		f.mu.Unlock()
		switch verb := strings.ToUpper(strings.Fields(cmd + " x")[0]); verb {
		case "EHLO", "HELO":
			if mode == "plaintext" {
				reply("250-fake\r\n250 AUTH PLAIN")
			} else {
				reply("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			}
		case "STARTTLS":
			if mode == "starttls-refused" {
				reply("454 4.7.0 TLS not available")
				continue
			}
			reply("220 go ahead")
			_ = tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}}).Handshake()
			return
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (f *fakeSMTP) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeSMTP) mailer() *smtpMailer {
	_, port, _ := net.SplitHostPort(f.ln.Addr().String())
	return &smtpMailer{host: "127.0.0.1", port: port, user: "resend", pass: "s3cret-key", from: "Tolon <noreply@example.test>"}
}

func selfSignedCert() tls.Certificate {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake smtp"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

var _ = Describe("SMTP delivery", func() {
	var server *fakeSMTP

	sendAndInspect := func() error {
		err := server.mailer().send("voter@example.test", "Your code", "Your code is 123456.\n")
		cmds := strings.Join(server.received(), "\n")
		Expect(cmds).NotTo(ContainSubstring("AUTH"), "credentials sent before TLS")
		Expect(cmds).NotTo(ContainSubstring("MAIL FROM"))
		Expect(cmds).NotTo(ContainSubstring("s3cret-key"))
		return err
	}

	Context("when the server does not offer STARTTLS", func() {
		BeforeEach(func() { server = startFakeSMTP("plaintext") })

		It("refuses to send in plaintext", func() {
			Expect(sendAndInspect()).To(MatchError(errNoSTARTTLS))
		})
	})

	Context("when the server offers STARTTLS but refuses it (downgrade)", func() {
		BeforeEach(func() { server = startFakeSMTP("starttls-refused") })

		It("stops instead of falling back to plaintext", func() {
			err := sendAndInspect()
			var tp *textproto.Error
			Expect(errors.As(err, &tp)).To(BeTrue())
			Expect(tp.Code).To(Equal(454))
		})
	})

	Context("when the server certificate does not verify", func() {
		BeforeEach(func() { server = startFakeSMTP("starttls-untrusted") })

		It("refuses the connection", func() {
			err := sendAndInspect()
			Expect(err).To(HaveOccurred())
			Expect(smtpErrClass(err)).To(Equal("tls error"))
		})
	})
})
