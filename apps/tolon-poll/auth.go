package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// verifier checks Cloudflare Access JWTs (RS256) against the team's published signing keys.
type verifier struct {
	certsURL string
	issuer   string
	audience string
	client   *http.Client
	now      func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	lastFetch time.Time
}

func newVerifier(teamDomain, audience string) *verifier {
	iss := strings.TrimRight(teamDomain, "/")
	return &verifier{
		certsURL: iss + "/cdn-cgi/access/certs",
		issuer:   iss,
		audience: audience,
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
		keys:     map[string]*rsa.PublicKey{},
	}
}

func (v *verifier) refresh() error {
	resp, err := v.client.Get(v.certsURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("certs: status %d", resp.StatusCode)
	}
	var body struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range body.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return errors.New("certs: no RSA keys")
	}
	v.mu.Lock()
	v.keys, v.lastFetch = keys, v.now()
	v.mu.Unlock()
	return nil
}

// key returns the key for kid, refetching at most every 30s when the kid is unknown (key rotation).
func (v *verifier) key(kid string) *rsa.PublicKey {
	v.mu.RLock()
	k, stale := v.keys[kid], v.now().Sub(v.lastFetch) > 30*time.Second
	v.mu.RUnlock()
	if k == nil && stale {
		_ = v.refresh()
		v.mu.RLock()
		k = v.keys[kid]
		v.mu.RUnlock()
	}
	return k
}

var errUnauthorized = errors.New("unauthorized")

// verify returns the lowercased email claim of a valid token.
func (v *verifier) verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errUnauthorized
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if decodeSegment(parts[0], &hdr) != nil || hdr.Alg != "RS256" {
		return "", errUnauthorized
	}
	key := v.key(hdr.Kid)
	if key == nil {
		return "", errUnauthorized
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errUnauthorized
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return "", errUnauthorized
	}
	var c struct {
		Aud   audience `json:"aud"`
		Iss   string   `json:"iss"`
		Exp   *int64   `json:"exp"`
		Nbf   *int64   `json:"nbf"`
		Email string   `json:"email"`
	}
	if decodeSegment(parts[1], &c) != nil {
		return "", errUnauthorized
	}
	now := v.now().Unix()
	const leeway = 30
	switch {
	case c.Iss != v.issuer, !c.Aud.has(v.audience):
	case c.Exp == nil || now > *c.Exp+leeway:
	case c.Nbf != nil && now+leeway < *c.Nbf:
	case c.Email == "" || !strings.Contains(c.Email, "@"):
	default:
		return strings.ToLower(strings.TrimSpace(c.Email)), nil
	}
	return "", errUnauthorized
}

func decodeSegment(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// audience accepts the JWT "aud" claim as a string or an array of strings.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*a = audience{s}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a))
}

func (a audience) has(want string) bool {
	for _, s := range a {
		if want != "" && s == want {
			return true
		}
	}
	return false
}

func tokenFrom(r *http.Request) string {
	if t := r.Header.Get("Cf-Access-Jwt-Assertion"); t != "" {
		return t
	}
	if c, err := r.Cookie("CF_Authorization"); err == nil {
		return c.Value
	}
	return ""
}
