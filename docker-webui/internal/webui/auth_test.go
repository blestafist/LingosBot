package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const authTestPassword = "correct horse battery staple"

func newAuthTestApp(t *testing.T) *app {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = initDB(db); err != nil {
		t.Fatal(err)
	}
	if err = initRunnerDB(db); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	x := &app{db: db, aead: aead, admin: "admin", password: authTestPassword}
	if err = x.bootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	return x
}

func authTestUser(t *testing.T, x *app, email string, admin bool) int64 {
	t.Helper()
	result, err := x.db.Exec(`INSERT INTO users(email,password_hash,admin,created_at) SELECT ?,password_hash,?,? FROM users WHERE id=1`, email, admin, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func authTestSession(t *testing.T, x *app, id int64) (string, string) {
	t.Helper()
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.db.Exec(`INSERT INTO user_sessions(token,csrf,user_id,expires) VALUES(?,?,?,?)`, tokenHash(token), csrf, id, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	return token, csrf
}

func authTestRequest(handler http.HandlerFunc, method, path, body, token, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestAuthPasswords(t *testing.T) {
	password := strings.Repeat("ж", 12)
	hash, err := hashPassword(password)
	if err != nil || !checkPassword(hash, password) || checkPassword(hash, password+"x") {
		t.Fatalf("password hashing failed: %v", err)
	}
	other, err := hashPassword(password)
	if err != nil || string(hash) == string(other) || strings.Contains(string(hash), password) {
		t.Fatal("password hash is not salted")
	}
	for _, invalid := range []string{"short", strings.Repeat("a", 1025), string([]byte{0xff})} {
		if _, err := hashPassword(invalid); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	if checkPassword(nil, password) || checkPassword([]byte{1}, password) {
		t.Fatal("invalid hash accepted")
	}
	long := strings.Repeat("a", 1024)
	hash, err = hashPassword(long)
	if err != nil || !checkPassword(hash, long) || checkPassword(hash, long[:1023]+"b") {
		t.Fatal("long password was truncated")
	}
}

func TestAuthLoginAndSession(t *testing.T) {
	x := newAuthTestApp(t)
	t.Setenv("COOKIE_SECURE", "1")
	form := url.Values{"email": {" ADMIN "}, "password": {authTestPassword}}
	r := httptest.NewRequest(http.MethodPost, "https://example.com/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	x.login(w, r)
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || len(cookie.Value) != 43 {
		t.Fatalf("unsafe cookie: %#v", cookie)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	u, ok := x.user(r)
	token, csrf := x.session(r)
	if !ok || u.ID != 1 || !u.Admin || token != cookie.Value || csrf == "" {
		t.Fatal("session did not identify user")
	}
	var stored string
	if err := x.db.QueryRow(`SELECT token FROM user_sessions`).Scan(&stored); err != nil || stored == token || stored != tokenHash(token) {
		t.Fatal("session token was not hashed")
	}
	if _, err := x.db.Exec(`UPDATE user_sessions SET expires=0`); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.user(r); ok {
		t.Fatal("expired session accepted")
	}
}

func TestAuthLoginGenericErrorsAndOrigin(t *testing.T) {
	x := newAuthTestApp(t)
	var generic string
	for _, email := range []string{"admin", "missing@example.com"} {
		form := url.Values{"email": {email}, "password": {"wrong"}}
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		x.login(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login status: %d", w.Code)
		}
		if generic != "" && generic != w.Body.String() {
			t.Fatal("login enumerates users")
		}
		generic = w.Body.String()
	}
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	x.login(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-origin login accepted")
	}
	r = httptest.NewRequest(http.MethodPost, "/login?email=admin&password="+url.QueryEscape(authTestPassword), nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	x.login(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("login credentials read from query")
	}
}

func TestAuthCSRF(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, test := range []struct {
		method, path, body, header, contentType string
		status                                  int
	}{
		{"POST", "/logout?csrf=" + csrf, "", "", "application/x-www-form-urlencoded", 403},
		{"POST", "/logout", "csrf=" + csrf, "", "application/x-www-form-urlencoded", 200},
		{"PATCH", "/api/account?csrf=" + csrf, "", "", "application/json", 403},
		{"PATCH", "/api/account", "", csrf, "application/json", 200},
		{"GET", "/api/account", "", "", "", 200},
	} {
		r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		r.Header.Set("Content-Type", test.contentType)
		r.Header.Set("X-CSRF-Token", test.header)
		w := httptest.NewRecorder()
		_, ok := x.apiUser(w, r)
		if w.Code != test.status || ok != (test.status == 200) {
			t.Fatalf("%s %s: %d %v", test.method, test.path, w.Code, ok)
		}
	}
	w := authTestRequest(x.logout, "POST", "/logout", "", token, csrf)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", w.Code)
	}
	if w = authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != http.StatusUnauthorized {
		t.Fatal("logout did not revoke session")
	}
}

func TestAuthCredentialIsolationAndAAD(t *testing.T) {
	x := newAuthTestApp(t)
	id := authTestUser(t, x, "second@example.com", false)
	a, ac := authTestSession(t, x, 1)
	b, bc := authTestSession(t, x, id)
	for _, test := range []struct{ token, csrf, email, password string }{
		{a, ac, "first@lingos.example", "secret-one"},
		{b, bc, "second@lingos.example", "secret-two"},
	} {
		body := fmt.Sprintf(`{"email":%q,"password":%q}`, test.email, test.password)
		w := authTestRequest(x.credentialsAPI, "PUT", "/api/credentials", body, test.token, test.csrf)
		if w.Code != 200 || strings.Contains(w.Body.String(), test.password) {
			t.Fatalf("save credentials: %d %s", w.Code, w.Body.String())
		}
	}
	email, password, err := x.loadCredentials(id)
	if err != nil || email != "second@lingos.example" || password != "secret-two" {
		t.Fatalf("load own credentials: %q %q %v", email, password, err)
	}
	w := authTestRequest(x.credentialsAPI, "GET", "/api/credentials", "", a, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "second@") || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("credential isolation: %s", w.Body.String())
	}
	if _, err = x.db.Exec(`UPDATE user_credentials SET password=(SELECT password FROM user_credentials WHERE user_id=1) WHERE user_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.loadCredentials(id); err == nil {
		t.Fatal("cross-user ciphertext swap accepted")
	}
	w = authTestRequest(x.credentialsAPI, "DELETE", "/api/credentials", "", a, ac)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, _, err := x.loadCredentials(1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("credentials not deleted")
	}
}

func TestAuthAdminLockoutAndRevocation(t *testing.T) {
	x := newAuthTestApp(t)
	a, ac := authTestSession(t, x, 1)
	for _, body := range []string{`{"disabled":true}`, `{"admin":false}`} {
		w := authTestRequest(x.adminUsersAPI, "PATCH", "/api/admin/users/1", body, a, ac)
		if w.Code != http.StatusConflict {
			t.Fatalf("last admin lockout: %d %s", w.Code, w.Body.String())
		}
	}
	body := `{"email":"Second@Example.com","password":"another secure password","admin":true}`
	w := authTestRequest(x.adminUsersAPI, "POST", "/api/admin/users", body, a, ac)
	var second User
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &second) != nil || second.Email != "second@example.com" || !second.Admin {
		t.Fatalf("create admin: %d %s", w.Code, w.Body.String())
	}
	if w = authTestRequest(x.adminUsersAPI, "POST", "/api/admin/users", body, a, ac); w.Code != http.StatusConflict {
		t.Fatal("duplicate email accepted")
	}
	b, bc := authTestSession(t, x, second.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,created_at) VALUES(1,'run','','manual','running',?)`, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	x.cancels = map[int64]context.CancelFunc{runID: cancel}
	w = authTestRequest(x.adminUsersAPI, "PATCH", "/api/admin/users/1", `{"disabled":true}`, b, bc)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if ctx.Err() == nil {
		t.Fatal("disabled user's background run was not canceled")
	}
	if w = authTestRequest(x.accountAPI, "GET", "/api/account", "", a, ""); w.Code != 401 {
		t.Fatal("disabled user session accepted")
	}
	if w = authTestRequest(x.adminUsersAPI, "PATCH", fmt.Sprintf("/api/admin/users/%d", second.ID), `{"admin":false}`, b, bc); w.Code != 409 {
		t.Fatal("disabled admin counted as active")
	}
	id := authTestUser(t, x, "ordinary@example.com", false)
	c, cc := authTestSession(t, x, id)
	if w = authTestRequest(x.adminUsersAPI, "GET", "/api/admin/users", "", c, ""); w.Code != 403 {
		t.Fatal("ordinary user accessed admin API")
	}
	w = authTestRequest(x.adminUsersAPI, "PATCH", fmt.Sprintf("/api/admin/users/%d", id), `{"password":"new secure password"}`, b, bc)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = authTestRequest(x.credentialsAPI, "DELETE", "/api/credentials", "", c, cc); w.Code != 401 {
		t.Fatal("admin password reset did not revoke session")
	}
}

func TestAuthAccountPasswordChange(t *testing.T) {
	x := newAuthTestApp(t)
	a, ac := authTestSession(t, x, 1)
	b, _ := authTestSession(t, x, 1)
	w := authTestRequest(x.accountAPI, "PATCH", "/api/account", `{"current_password":"incorrect","password":"replacement password"}`, a, ac)
	if w.Code != 401 {
		t.Fatal("password changed without current password")
	}
	body := fmt.Sprintf(`{"current_password":%q,"password":"replacement password"}`, authTestPassword)
	w = authTestRequest(x.accountAPI, "PATCH", "/api/account", body, a, ac)
	if w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	for _, token := range []string{a, b} {
		if w = authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != 401 {
			t.Fatal("password change did not revoke all sessions")
		}
	}
	x.password = "environment password changed"
	if err := x.bootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	var hash []byte
	if err := x.db.QueryRow(`SELECT password_hash FROM users WHERE id=1`).Scan(&hash); err != nil || !checkPassword(hash, "replacement password") {
		t.Fatal("bootstrap reset existing password")
	}
}

func TestAuthConcurrentAdminDemotion(t *testing.T) {
	x := newAuthTestApp(t)
	id := authTestUser(t, x, "other-admin@example.com", true)
	a, ac := authTestSession(t, x, 1)
	b, bc := authTestSession(t, x, id)
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, test := range []struct {
		id          int64
		token, csrf string
	}{{1, a, ac}, {id, b, bc}} {
		go func() {
			responses <- authTestRequest(x.adminUsersAPI, "PATCH", fmt.Sprintf("/api/admin/users/%d", test.id), `{"admin":false}`, test.token, test.csrf)
		}()
	}
	statuses := map[int]int{}
	for range 2 {
		w := <-responses
		statuses[w.Code]++
	}
	var active int
	if err := x.db.QueryRow(`SELECT count(*) FROM users WHERE admin=1 AND disabled=0`).Scan(&active); err != nil || active != 1 || statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("concurrent demotions: active=%d statuses=%v err=%v", active, statuses, err)
	}
}

func TestAuthLegacyMigration(t *testing.T) {
	x := newAuthTestApp(t)
	if _, err := x.db.Exec(`DELETE FROM auth_meta; DELETE FROM users; INSERT INTO sessions VALUES('legacy','csrf',9999999999)`); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, x.aead.NonceSize())
	legacy := x.aead.Seal(nonce, nonce, []byte("legacy secret"), nil)
	if _, err := x.db.Exec(`INSERT INTO credentials VALUES(1,'legacy@example.com',?)`, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := initDB(x.db); err != nil {
			t.Fatal(err)
		}
		if err := x.bootstrapAdmin(); err != nil {
			t.Fatal(err)
		}
	}
	email, password, err := x.loadCredentials(1)
	if err != nil || email != "legacy@example.com" || password != "legacy secret" {
		t.Fatalf("migration lost credentials: %q %q %v", email, password, err)
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy sessions survived migration")
	}
	if _, err = x.db.Exec(`DELETE FROM user_credentials`); err != nil {
		t.Fatal(err)
	}
	if err = x.bootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.loadCredentials(1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart resurrected deleted legacy credentials")
	}
}

func TestAuthMigrationRollback(t *testing.T) {
	x := newAuthTestApp(t)
	if _, err := x.db.Exec(`DELETE FROM auth_meta; DELETE FROM users; INSERT INTO credentials VALUES(1,'legacy@example.com',x'00')`); err != nil {
		t.Fatal(err)
	}
	if err := x.bootstrapAdmin(); err == nil {
		t.Fatal("corrupt legacy credentials silently discarded")
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration was not transactional")
	}
}

func TestAuthRateLimitPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	x := newAuthTestApp(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	x.db = db
	if err = initDB(db); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	for i := range 10 {
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		if err := x.loginLimit(r, "same@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	x.db = db
	if err = initDB(db); err != nil {
		t.Fatal(err)
	}
	r.RemoteAddr = "192.0.2.1:5678"
	r.Header.Set("X-Forwarded-For", "203.0.113.1")
	if err := x.loginLimit(r, "same@example.com"); !errors.Is(err, errRateLimited) {
		t.Fatal("IP/email rate limit did not survive restart or X-Forwarded-For spoof")
	}
	r.RemoteAddr = "192.0.2.2:5678"
	if err := x.loginLimit(r, "same@example.com"); err != nil {
		t.Fatalf("another IP was blocked by email rate limit: %v", err)
	}
	r.RemoteAddr = "192.0.2.3:1234"
	for i := range 30 {
		if err := x.loginLimit(r, fmt.Sprintf("user%d@example.com", i)); err != nil {
			t.Fatal(err)
		}
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.2")
	if err := x.loginLimit(r, "new@example.com"); !errors.Is(err, errRateLimited) {
		t.Fatal("X-Forwarded-For bypassed IP rate limit")
	}
	if _, err := db.Exec(`UPDATE auth_limits SET expires=0`); err != nil {
		t.Fatal(err)
	}
	if err := x.loginLimit(r, "new@example.com"); err != nil {
		t.Fatalf("expired rate limit not reset: %v", err)
	}
}

func TestAuthRateLimitCapacityAndHTTP(t *testing.T) {
	x := newAuthTestApp(t)
	form := url.Values{"email": {" ADMIN "}, "password": {authTestPassword}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for range 10 {
		if err := x.loginLimit(r, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	x.login(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("login rate limit: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`DELETE FROM auth_limits; WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<10000) INSERT INTO auth_limits SELECT 'test:'||i,1,? FROM n`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := x.loginLimit(r, "new@example.com"); !errors.Is(err, errRateLimited) {
		t.Fatal("rate limit storage capacity was not enforced")
	}
	if _, err := x.db.Exec(`UPDATE auth_limits SET expires=0`); err != nil {
		t.Fatal(err)
	}
	if err := x.loginLimit(r, "new@example.com"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM auth_limits`).Scan(&count); err != nil || count != 9902 {
		t.Fatalf("cleanup was not bounded to 100 rows: count=%d err=%v", count, err)
	}
}

func TestAuthSuccessfulLoginRefundsOnlyReservedAttempts(t *testing.T) {
	x := newAuthTestApp(t)
	r := httptest.NewRequest("POST", "/login", nil)
	for range 3 {
		for _, email := range []string{"admin", "victim@example.com"} {
			if err := x.loginLimit(r, email); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 11 {
		form := url.Values{"email": {"admin"}, "password": {authTestPassword}}
		r = httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		x.login(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("successful login exhausted budget: %d %s", w.Code, w.Body.String())
		}
	}
	ipKey, emailKey := loginLimitKeys(r, "admin")
	_, victimKey := loginLimitKeys(r, "victim@example.com")
	for key, want := range map[string]int{ipKey: 6, emailKey: 3, victimKey: 3} {
		var attempts int
		if err := x.db.QueryRow(`SELECT attempts FROM auth_limits WHERE key=?`, key).Scan(&attempts); err != nil || attempts != want {
			t.Fatalf("successful login changed earlier failures: key=%s attempts=%d want=%d err=%v", key, attempts, want, err)
		}
	}
}

func TestAuthStorageErrorsAndSecurityHeaders(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	handler := x.secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control", "X-Frame-Options"} {
		if w.Header().Get(header) == "" {
			t.Fatalf("missing %s", header)
		}
	}
	r = httptest.NewRequest("PUT", "/api/credentials", nil)
	r.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("middleware accepted cross-origin write")
	}
	if _, err := x.db.Exec(`DROP TABLE user_credentials`); err != nil {
		t.Fatal(err)
	}
	if w := authTestRequest(x.credentialsAPI, "GET", "/api/credentials", "", token, ""); w.Code != 500 {
		t.Fatal("credential storage error ignored")
	}
	if err := x.db.Close(); err != nil {
		t.Fatal(err)
	}
	if w := authTestRequest(x.accountAPI, "PATCH", "/api/account", "{}", token, csrf); w.Code != 500 {
		t.Fatal("session storage error hidden as unauthorized")
	}
}

func TestAuthFailedSessionWriteDoesNotSetCookie(t *testing.T) {
	for name, trigger := range map[string]string{
		"session": `CREATE TRIGGER reject_session BEFORE INSERT ON user_sessions BEGIN SELECT RAISE(ABORT,'test failure'); END`,
		"refund":  `CREATE TRIGGER reject_refund BEFORE UPDATE ON auth_limits WHEN NEW.attempts<OLD.attempts BEGIN SELECT RAISE(ABORT,'test failure'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			x := newAuthTestApp(t)
			if _, err := x.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"email": {"admin"}, "password": {authTestPassword}}
			r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			x.login(w, r)
			if w.Code != 500 || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "test failure") {
				t.Fatalf("failed session write: %d %s", w.Code, w.Body.String())
			}
			var count int
			if err := x.db.QueryRow(`SELECT count(*) FROM user_sessions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed login created a session")
			}
			if err := x.db.QueryRow(`SELECT count(*) FROM auth_limits WHERE attempts=1`).Scan(&count); err != nil || count != 2 {
				t.Fatal("failed login refunded reserved attempts")
			}
		})
	}
}
