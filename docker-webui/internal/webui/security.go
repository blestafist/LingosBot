package webui

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"
)

const passwordIterations = 600000

func validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 12 && len(password) <= 1024
}

// The version fixes the algorithm, work factor, salt size and key size.
func hashPassword(password string) ([]byte, error) {
	if !validPassword(password) {
		return nil, fmt.Errorf("password must be 12 or more characters and at most 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{1}, salt...), key...), nil
}

func checkPassword(hash []byte, password string) bool {
	// Unknown users do the same expensive work as existing users.
	valid := len(hash) == 49 && hash[0] == 1
	if !valid {
		hash = make([]byte, 49)
	}
	key, err := pbkdf2.Key(sha256.New, password, hash[1:17], passwordIterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, hash[17:]) == 1 && valid
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func cookieSecure() bool {
	v := os.Getenv("COOKIE_SECURE")
	return v == "1" || strings.EqualFold(v, "true")
}

func sameOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	u, err := url.Parse(origins[0])
	scheme := "http"
	if r.TLS != nil || cookieSecure() {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func (x *app) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !sameOrigin(r) {
			apiError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
