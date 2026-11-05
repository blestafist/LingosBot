package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setRegistrationTestMode(t *testing.T, x *app, mode string) {
	t.Helper()
	if _, err := x.db.Exec(`UPDATE registration_policy SET mode=? WHERE id=1`, mode); err != nil {
		t.Fatal(err)
	}
}

func registrationTestRequest(x *app, email, password string) *httptest.ResponseRecorder {
	form := url.Values{"email": {email}, "password": {password}}
	r := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	x.routes().ServeHTTP(w, r)
	return w
}

func TestRegistrationMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registration.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if _, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY,email TEXT NOT NULL UNIQUE COLLATE NOCASE,password_hash BLOB NOT NULL,
		admin INTEGER NOT NULL DEFAULT 0,disabled INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
		INSERT INTO users VALUES(1,'admin@example.com',x'00',1,0,123),(9,'disabled@example.com',x'00',0,1,456)`); err != nil {
		t.Fatal(err)
	}
	if err = initDB(db); err != nil {
		t.Fatal(err)
	}
	x := &app{db: db}
	mode, err := x.registrationMode(t.Context())
	if err != nil || mode != registrationAdminOnly {
		t.Fatalf("default: %q %v", mode, err)
	}
	var existing int
	if err = db.QueryRow(`SELECT count(*) FROM users WHERE pending=0 AND ((id=1 AND admin=1 AND disabled=0 AND created_at=123) OR (id=9 AND admin=0 AND disabled=1 AND created_at=456))`).Scan(&existing); err != nil || existing != 2 {
		t.Fatalf("migration changed existing users: %d %v", existing, err)
	}
	setRegistrationTestMode(t, x, registrationApproval)
	if _, err = db.Exec(`INSERT INTO users(id,email,password_hash,disabled,pending,created_at) VALUES(10,'pending@example.com',x'00',1,1,789)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	x.db = db
	for range 2 {
		if err = initDB(db); err != nil {
			t.Fatal(err)
		}
	}
	mode, err = x.registrationMode(t.Context())
	if err != nil || mode != registrationApproval {
		t.Fatalf("policy did not persist: %q %v", mode, err)
	}
	var pending, disabled bool
	if err = db.QueryRow(`SELECT pending,disabled FROM users WHERE id=10`).Scan(&pending, &disabled); err != nil || !pending || !disabled {
		t.Fatalf("pending did not persist: %v %v %v", pending, disabled, err)
	}
	for _, query := range []string{
		`UPDATE registration_policy SET mode='unknown'`,
		`UPDATE users SET disabled=0 WHERE id=10`,
	} {
		if _, err = db.Exec(query); err == nil {
			t.Fatalf("unsafe state accepted: %s", query)
		}
	}
}

func TestRegistrationModesAndDuplicates(t *testing.T) {
	for _, mode := range []string{registrationAdminOnly, registrationApproval, registrationOpen} {
		t.Run(mode, func(t *testing.T) {
			x := integrationApp(t)
			setRegistrationTestMode(t, x, mode)
			w := registrationTestRequest(x, " Learner@Example.com ", authTestPassword)
			if mode == registrationAdminOnly {
				if w.Code != http.StatusForbidden {
					t.Fatalf("closed registration: %d %s", w.Code, w.Body.String())
				}
				var count int
				if err := x.db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("closed registration created a user: %d %v", count, err)
				}
				return
			}
			want := fmt.Sprintf("{\"pending\":%t}\n", mode == registrationApproval)
			if w.Code != http.StatusCreated || w.Body.String() != want || len(w.Result().Cookies()) != 0 {
				t.Fatalf("registration: %d %s", w.Code, w.Body.String())
			}
			var u User
			var hash []byte
			if err := x.db.QueryRow(`SELECT id,email,admin,disabled,pending,password_hash FROM users WHERE email='learner@example.com'`).Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending, &hash); err != nil || u.Email != "learner@example.com" || u.Admin || u.Pending != (mode == registrationApproval) || u.Disabled != u.Pending || !checkPassword(hash, authTestPassword) {
				t.Fatalf("registered user: %+v %v", u, err)
			}
			var before int64
			if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for _, email := range []string{"LEARNER@EXAMPLE.COM", "admin@example.com"} {
				w = registrationTestRequest(x, email, "different secure password")
				if w.Code != http.StatusCreated || w.Body.String() != want || len(w.Result().Cookies()) != 0 {
					t.Fatalf("duplicate enumerates users: %d %s", w.Code, w.Body.String())
				}
			}
			var count, sessions int
			var after int64
			var unchanged []byte
			if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM user_sessions),(SELECT last_id FROM user_id_counter),password_hash FROM users WHERE id=?`, u.ID).Scan(&count, &sessions, &after, &unchanged); err != nil || count != 2 || sessions != 0 || before != after || string(hash) != string(unchanged) {
				t.Fatalf("duplicate changed accounts: count=%d sessions=%d counter=%d/%d err=%v", count, sessions, before, after, err)
			}
			form := url.Values{"email": {u.Email}, "password": {authTestPassword}}
			r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w = httptest.NewRecorder()
			x.login(w, r)
			wantStatus := http.StatusSeeOther
			if u.Pending {
				wantStatus = http.StatusUnauthorized
			}
			if w.Code != wantStatus {
				t.Fatalf("registered user login: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRegistrationInputAndMethods(t *testing.T) {
	x := newAuthTestApp(t)
	setRegistrationTestMode(t, x, registrationOpen)
	valid := url.Values{"email": {"learner@example.com"}, "password": {authTestPassword}}.Encode()
	for _, test := range []struct {
		name, method, path, body, origin string
		want                             int
	}{
		{"get", "GET", "/register", "", "", 405},
		{"put", "PUT", "/register", valid, "", 405},
		{"delete", "DELETE", "/register", valid, "", 405},
		{"origin", "POST", "/register", valid, "https://evil.example", 403},
		{"null origin", "POST", "/register", valid, "null", 403},
		{"empty", "POST", "/register", "", "", 400},
		{"query only", "POST", "/register?" + valid, "", "", 400},
		{"email", "POST", "/register", "email=invalid&password=" + url.QueryEscape(authTestPassword), "", 400},
		{"display name", "POST", "/register", "email=" + url.QueryEscape("User <a@example.com>") + "&password=" + url.QueryEscape(authTestPassword), "", 400},
		{"email too long", "POST", "/register", "email=" + strings.Repeat("a", 255) + "&password=" + url.QueryEscape(authTestPassword), "", 400},
		{"short password", "POST", "/register", "email=a@example.com&password=short", "", 400},
		{"long password", "POST", "/register", "email=a@example.com&password=" + strings.Repeat("a", 1025), "", 400},
		{"invalid utf8", "POST", "/register", "email=a@example.com&password=%FF" + strings.Repeat("a", 12), "", 400},
		{"malformed form", "POST", "/register", valid + "&x=%", "", 400},
		{"large body", "POST", "/register", valid + "&x=" + strings.Repeat("a", 1<<20), "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			w := httptest.NewRecorder()
			x.routes().ServeHTTP(w, r)
			if w.Code != test.want || len(w.Result().Cookies()) != 0 {
				t.Fatalf("status=%d want=%d: %s", w.Code, test.want, w.Body.String())
			}
		})
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid request created a user: %d %v", count, err)
	}
}

func TestRegistrationRateLimit(t *testing.T) {
	x := newAuthTestApp(t)
	setRegistrationTestMode(t, x, registrationOpen)
	r := httptest.NewRequest(http.MethodPost, "/register", nil)
	for range 10 {
		if err := x.loginLimit(r, "same@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	w := registrationTestRequest(x, " SAME@EXAMPLE.COM ", authTestPassword)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "900" {
		t.Fatalf("email limit: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`UPDATE auth_limits SET attempts=30 WHERE key LIKE 'ip:%'`); err != nil {
		t.Fatal(err)
	}
	w = registrationTestRequest(x, "new@example.com", authTestPassword)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("IP limit: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`UPDATE auth_limits SET expires=0`); err != nil {
		t.Fatal(err)
	}
	w = registrationTestRequest(x, "new@example.com", authTestPassword)
	if w.Code != http.StatusCreated {
		t.Fatalf("expired limit: %d %s", w.Code, w.Body.String())
	}
	var attempts int
	if err := x.db.QueryRow(`SELECT attempts FROM auth_limits WHERE key LIKE 'ip:%'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("successful registration refunded rate limit: %d %v", attempts, err)
	}
}

func TestRegistrationConcurrentDuplicate(t *testing.T) {
	x := newAuthTestApp(t)
	setRegistrationTestMode(t, x, registrationApproval)
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, email := range []string{"learner@example.com", "LEARNER@EXAMPLE.COM"} {
		go func() {
			<-start
			responses <- registrationTestRequest(x, email, authTestPassword)
		}()
	}
	close(start)
	for range 2 {
		w := <-responses
		if w.Code != 201 || strings.TrimSpace(w.Body.String()) != `{"pending":true}` {
			t.Fatalf("concurrent registration: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	var lastID int64
	if err := x.db.QueryRow(`SELECT count(*),(SELECT last_id FROM user_id_counter) FROM users`).Scan(&count, &lastID); err != nil || count != 2 || lastID != 2 {
		t.Fatalf("duplicate registration: users=%d last_id=%d err=%v", count, lastID, err)
	}
}

func TestRegistrationAdminPolicyGuards(t *testing.T) {
	x := newAuthTestApp(t)
	a, ac := authTestSession(t, x, 1)
	id := authTestUser(t, x, "member@example.com", false)
	b, bc := authTestSession(t, x, id)
	for _, test := range []struct {
		name, method, body, token, csrf, origin string
		want                                    int
	}{
		{"anonymous get", "GET", "", "", "", "", 401},
		{"anonymous put", "PUT", `{"mode":"open"}`, "", "", "", 401},
		{"member get", "GET", "", b, "", "", 403},
		{"member put", "PUT", `{"mode":"open"}`, b, bc, "", 403},
		{"no csrf", "PUT", `{"mode":"open"}`, a, "", "", 403},
		{"wrong csrf", "PUT", `{"mode":"open"}`, a, "wrong", "", 403},
		{"origin", "PUT", `{"mode":"open"}`, a, ac, "https://evil.example", 403},
		{"method", "PATCH", `{"mode":"open"}`, a, ac, "", 405},
		{"empty", "PUT", `{}`, a, ac, "", 400},
		{"null", "PUT", `null`, a, ac, "", 400},
		{"unknown mode", "PUT", `{"mode":"OPEN"}`, a, ac, "", 400},
		{"unknown field", "PUT", `{"mode":"open","admin":true}`, a, ac, "", 400},
		{"trailing json", "PUT", `{"mode":"open"}{}`, a, ac, "", 400},
		{"large body", "PUT", `{"mode":"open"}` + strings.Repeat(" ", 1<<20), a, ac, "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "/api/admin/registration", strings.NewReader(test.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", test.csrf)
			if test.token != "" {
				r.AddCookie(&http.Cookie{Name: "session", Value: test.token})
			}
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			w := httptest.NewRecorder()
			x.routes().ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d: %s", w.Code, test.want, w.Body.String())
			}
		})
	}
	for _, mode := range []string{registrationAdminOnly, registrationApproval, registrationOpen} {
		body := fmt.Sprintf(`{"mode":%q}`, mode)
		w := authTestRequest(x.routes().ServeHTTP, "PUT", "/api/admin/registration", body, a, ac)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != body {
			t.Fatalf("put: %d %s", w.Code, w.Body.String())
		}
		w = authTestRequest(x.routes().ServeHTTP, "GET", "/api/admin/registration", "", a, "")
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != body {
			t.Fatalf("get: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := x.db.Exec(`UPDATE users SET disabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if w := authTestRequest(x.registrationAPI, "PUT", "/api/admin/registration", `{"mode":"approval"}`, a, ac); w.Code != 401 {
		t.Fatalf("disabled admin changed policy: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistrationPendingIsolationAndApproval(t *testing.T) {
	x := integrationApp(t)
	setRegistrationTestMode(t, x, registrationApproval)
	if w := registrationTestRequest(x, "pending@example.com", authTestPassword); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var id int64
	if err := x.db.QueryRow(`SELECT id FROM users WHERE email='pending@example.com'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	token, csrf := authTestSession(t, x, id)
	for _, test := range []struct{ method, path, body string }{
		{"GET", "/api/account", ""},
		{"PATCH", "/api/account", `{"password":"replacement password"}`},
		{"PUT", "/api/credentials", `{"email":"a@example.com","password":"secret"}`},
		{"POST", "/api/runs", `{"mode":"run"}`},
		{"PUT", "/api/schedule", `{}`},
		{"PUT", "/api/admin/registration", `{"mode":"open"}`},
	} {
		if w := authTestRequest(x.routes().ServeHTTP, test.method, test.path, test.body, token, csrf); w.Code != 401 {
			t.Fatalf("pending access %s: %d %s", test.path, w.Code, w.Body.String())
		}
	}
	seedAuthDeleteData(t, x, id)
	if _, _, err := x.loadCredentials(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending loaded worker credentials: %v", err)
	}
	if runID, err := x.startRun(id, "run", "", "manual"); err == nil || runID != 0 {
		t.Fatalf("pending started worker: %d %v", runID, err)
	}
	if _, err := x.db.Exec(`UPDATE user_schedules SET next_run=1 WHERE user_id=?`, id); err != nil {
		t.Fatal(err)
	}
	x.runScheduled(time.Now())
	var due int64
	if err := x.db.QueryRow(`SELECT next_run FROM user_schedules WHERE user_id=?`, id).Scan(&due); err != nil || due != 1 {
		t.Fatalf("scheduler touched pending user: %d %v", due, err)
	}
	a, ac := authTestSession(t, x, 1)
	path := fmt.Sprintf("/api/admin/users/%d", id)
	for _, body := range []string{`{"disabled":false}`, `{"admin":true}`} {
		if w := authTestRequest(x.adminUsersAPI, "PATCH", path, body, a, ac); w.Code != 409 {
			t.Fatalf("bypassed approval: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"approve":false}`, `{"approve":true,"admin":true}`, `{"approve":true,"disabled":false}`, `{"approve":true,"password":"replacement password"}`} {
		if w := authTestRequest(x.adminUsersAPI, "PATCH", path, body, a, ac); w.Code != 400 {
			t.Fatalf("mixed approval accepted: %d %s", w.Code, w.Body.String())
		}
	}
	member := authTestUser(t, x, "member@example.com", false)
	b, bc := authTestSession(t, x, member)
	for _, test := range []struct {
		token, csrf string
		want        int
	}{{"", "", 401}, {b, bc, 403}, {a, "", 403}} {
		if w := authTestRequest(x.adminUsersAPI, "PATCH", path, `{"approve":true}`, test.token, test.csrf); w.Code != test.want {
			t.Fatalf("approval guard: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_approval BEFORE UPDATE OF pending ON users WHEN NEW.pending=0 AND OLD.pending=1 BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := authTestRequest(x.adminUsersAPI, "PATCH", path, `{"approve":true}`, a, ac)
	if w.Code != 500 || strings.Contains(w.Body.String(), "test failure") {
		t.Fatalf("failed approval: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`DROP TRIGGER reject_approval`); err != nil {
		t.Fatal(err)
	}
	setRegistrationTestMode(t, x, registrationOpen)
	w = registrationTestRequest(x, "PENDING@example.com", "different secure password")
	if w.Code != 201 || strings.TrimSpace(w.Body.String()) != `{"pending":false}` {
		t.Fatalf("duplicate response exposed pending state: %d %s", w.Code, w.Body.String())
	}
	var pending bool
	if err := x.db.QueryRow(`SELECT pending FROM users WHERE id=?`, id).Scan(&pending); err != nil || !pending {
		t.Fatalf("mode change auto-approved user: %v %v", pending, err)
	}
	w = authTestRequest(x.adminUsersAPI, "GET", "/api/admin/users", "", a, "")
	var list struct {
		Users []User `json:"users"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Users) != 3 || !list.Users[1].Pending {
		t.Fatalf("admin list omitted pending: %d %s", w.Code, w.Body.String())
	}
	w = authTestRequest(x.adminUsersAPI, "PATCH", path, `{"approve":true}`, a, ac)
	var approved User
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &approved) != nil || approved.Pending || approved.Disabled || approved.Admin || approved.ID != id {
		t.Fatalf("approval: %d %s", w.Code, w.Body.String())
	}
	if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != 401 {
		t.Fatal("approval resurrected a pending user's old session")
	}
	if w := authTestRequest(x.adminUsersAPI, "PATCH", path, `{"approve":true}`, a, ac); w.Code != 409 {
		t.Fatalf("non-pending approval: %d %s", w.Code, w.Body.String())
	}
	form := url.Values{"email": {"pending@example.com"}, "password": {authTestPassword}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	x.login(w, r)
	if w.Code != 303 {
		t.Fatalf("approved user cannot login: %d %s", w.Code, w.Body.String())
	}
	setRegistrationTestMode(t, x, registrationApproval)
	if w := registrationTestRequest(x, "reject@example.com", authTestPassword); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rejectedID int64
	if err := x.db.QueryRow(`SELECT id FROM users WHERE email='reject@example.com'`).Scan(&rejectedID); err != nil {
		t.Fatal(err)
	}
	w = authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", rejectedID), "", a, ac)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"deleted":true}` {
		t.Fatalf("rejection: %d %s", w.Code, w.Body.String())
	}
	var exists bool
	if err := x.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, rejectedID).Scan(&exists); err != nil || exists {
		t.Fatalf("rejected account survived: %v %v", exists, err)
	}
}

type registrationTestBody struct {
	io.Reader
	before func()
}

func (b *registrationTestBody) Read(p []byte) (int, error) {
	if b.before != nil {
		before := b.before
		b.before = nil
		before()
	}
	return b.Reader.Read(p)
}

func TestRegistrationStaleAdminRecheck(t *testing.T) {
	for _, change := range []string{"admin=0", "disabled=1"} {
		for _, operation := range []string{"policy", "approval"} {
			t.Run(change+"/"+operation, func(t *testing.T) {
				x := newAuthTestApp(t)
				id := authTestUser(t, x, "pending@example.com", false)
				if _, err := x.db.Exec(`UPDATE users SET disabled=1,pending=1 WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
				a, ac := authTestSession(t, x, 1)
				method, path, body := "PUT", "/api/admin/registration", `{"mode":"open"}`
				if operation == "approval" {
					method, path, body = "PATCH", fmt.Sprintf("/api/admin/users/%d", id), `{"approve":true}`
				}
				reader := &registrationTestBody{Reader: strings.NewReader(body), before: func() {
					if _, err := x.db.Exec(`UPDATE users SET ` + change + ` WHERE id=1`); err != nil {
						t.Fatal(err)
					}
				}}
				r := httptest.NewRequest(method, path, reader)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-CSRF-Token", ac)
				r.AddCookie(&http.Cookie{Name: "session", Value: a})
				w := httptest.NewRecorder()
				x.routes().ServeHTTP(w, r)
				if w.Code != 403 {
					t.Fatalf("stale admin accepted: %d %s", w.Code, w.Body.String())
				}
				mode, err := x.registrationMode(t.Context())
				var pending bool
				if err != nil || mode != registrationAdminOnly {
					t.Fatalf("stale admin changed policy: %q %v", mode, err)
				}
				if err := x.db.QueryRow(`SELECT pending FROM users WHERE id=?`, id).Scan(&pending); err != nil || !pending {
					t.Fatalf("stale admin approved user: %v %v", pending, err)
				}
			})
		}
	}
}

func TestRegistrationPolicyRecheckAndRollback(t *testing.T) {
	for _, mode := range []string{registrationAdminOnly, registrationApproval} {
		t.Run(mode, func(t *testing.T) {
			x := newAuthTestApp(t)
			setRegistrationTestMode(t, x, registrationOpen)
			if _, err := x.db.Exec(`CREATE TRIGGER change_policy AFTER INSERT ON auth_limits WHEN NEW.key LIKE 'email:%' BEGIN UPDATE registration_policy SET mode='` + mode + `'; END`); err != nil {
				t.Fatal(err)
			}
			w := registrationTestRequest(x, "learner@example.com", authTestPassword)
			if mode == registrationAdminOnly {
				if w.Code != 403 {
					t.Fatalf("registration ignored closure: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 201 || strings.TrimSpace(w.Body.String()) != `{"pending":true}` {
				t.Fatalf("registration ignored approval policy: %d %s", w.Code, w.Body.String())
			}
			var created int
			wantCreated := 0
			if mode == registrationApproval {
				wantCreated = 1
			}
			if err := x.db.QueryRow(`SELECT count(*) FROM users WHERE email='learner@example.com' AND pending=1 AND disabled=1`).Scan(&created); err != nil || created != wantCreated {
				t.Fatalf("policy race: created=%d err=%v", created, err)
			}
		})
	}
	x := newAuthTestApp(t)
	setRegistrationTestMode(t, x, registrationOpen)
	if _, err := x.db.Exec(`CREATE TRIGGER reject_registration BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := registrationTestRequest(x, "learner@example.com", authTestPassword)
	if w.Code != 500 || strings.Contains(w.Body.String(), "test failure") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("failed registration: %d %s", w.Code, w.Body.String())
	}
	if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter`).Scan(&after); err != nil || before != after {
		t.Fatalf("failed registration changed ID watermark: %d/%d %v", before, after, err)
	}
}

func TestRegistrationHomeAndStorageFailClosed(t *testing.T) {
	x := newAuthTestApp(t)
	oldPage := page
	page = template.Must(template.New("index.html").Parse(`{{.RegistrationMode}}`))
	t.Cleanup(func() { page = oldPage })
	a, ac := authTestSession(t, x, 1)
	for _, mode := range []string{registrationAdminOnly, registrationApproval, registrationOpen} {
		setRegistrationTestMode(t, x, mode)
		w := authTestRequest(x.home, "GET", "/", "", "", "")
		if w.Code != 200 || w.Body.String() != mode {
			t.Fatalf("home cached policy: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := x.db.Exec(`DELETE FROM registration_policy`); err != nil {
		t.Fatal(err)
	}
	if w := authTestRequest(x.home, "GET", "/", "", "", ""); w.Code != 200 || w.Body.String() != registrationAdminOnly {
		t.Fatalf("home did not fail closed: %d %s", w.Code, w.Body.String())
	}
	if w := registrationTestRequest(x, "learner@example.com", authTestPassword); w.Code != 500 {
		t.Fatalf("missing policy allowed registration: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "PUT"} {
		if w := authTestRequest(x.registrationAPI, method, "/api/admin/registration", `{"mode":"open"}`, a, ac); w.Code != 500 {
			t.Fatalf("missing policy %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}
