package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramAdminListAndDisconnect(t *testing.T) {
	x := telegramAPITestApp(t)
	first := authTestUser(t, x, "first@example.test", false)
	second := authTestUser(t, x, "second@example.test", false)
	telegramTestBind(t, x, first, 100)
	telegramTestBind(t, x, second, 200)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1 WHERE user_id=?`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, second); err != nil {
		t.Fatal(err)
	}
	telegramTestRun(t, x, first, "run", "completed")
	token, csrf := authTestSession(t, x, 1)
	w := authTestRequest(x.routes().ServeHTTP, "GET", "/api/admin/telegram", "", token, "")
	var response struct {
		Bindings []telegramBinding `json:"bindings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || len(response.Bindings) != 2 {
		t.Fatalf("list: %d %s err=%v", w.Code, w.Body.String(), err)
	}
	if response.Bindings[0].UserID != first || response.Bindings[0].Email != "first@example.test" || response.Bindings[0].ChatID != "100" || !response.Bindings[0].Notifications || !response.Bindings[1].Disabled {
		t.Fatalf("bindings: %+v", response.Bindings)
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatal("tokens in admin list")
	}
	path := fmt.Sprintf("/api/admin/telegram/%d", first)
	if w = authTestRequest(x.routes().ServeHTTP, "DELETE", path, "", token, csrf); w.Code != 200 {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	var links, queued, users int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_links`).Scan(&links); err != nil || links != 1 {
		t.Fatalf("remaining links=%d err=%v", links, err)
	}
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_outbox`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}
	if err := x.db.QueryRow(`SELECT count(*) FROM users WHERE id=?`, first).Scan(&users); err != nil || users != 1 {
		t.Fatal("disconnect deleted website user")
	}
	if w = authTestRequest(x.routes().ServeHTTP, "DELETE", path, "", token, csrf); w.Code != 200 {
		t.Fatalf("idempotent delete=%d", w.Code)
	}
	code := telegramTestCode(t, x, first)
	if w = authTestRequest(x.routes().ServeHTTP, "DELETE", path, "", token, csrf); w.Code != 200 {
		t.Fatal("cancel pending code")
	}
	var tokens int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_tokens WHERE token_hash=?`, tokenHash(code)).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("pending token=%d err=%v", tokens, err)
	}
}

func TestTelegramAdminPermissionsAndValidation(t *testing.T) {
	x := telegramAPITestApp(t)
	id := authTestUser(t, x, "learner@example.test", false)
	userToken, userCSRF := authTestSession(t, x, id)
	adminToken, adminCSRF := authTestSession(t, x, 1)
	for _, test := range []struct {
		method, path, token, csrf, origin string
		want                              int
	}{
		{"GET", "/api/admin/telegram", "", "", "", 401},
		{"GET", "/api/admin/telegram", userToken, "", "", 403},
		{"DELETE", "/api/admin/telegram/1", userToken, userCSRF, "", 403},
		{"DELETE", "/api/admin/telegram/1", adminToken, "", "", 403},
		{"DELETE", "/api/admin/telegram/1", adminToken, adminCSRF, "https://evil.example", 403},
		{"DELETE", "/api/admin/telegram", adminToken, adminCSRF, "", 400},
		{"DELETE", "/api/admin/telegram/", adminToken, adminCSRF, "", 400},
		{"DELETE", "/api/admin/telegram/0", adminToken, adminCSRF, "", 400},
		{"DELETE", "/api/admin/telegram/-1", adminToken, adminCSRF, "", 400},
		{"DELETE", "/api/admin/telegram/1/extra", adminToken, adminCSRF, "", 400},
		{"DELETE", "/api/admin/telegram/9223372036854775808", adminToken, adminCSRF, "", 400},
		{"PATCH", "/api/admin/telegram/1", adminToken, adminCSRF, "", 405},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		if test.token != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: test.token})
		}
		r.Header.Set("X-CSRF-Token", test.csrf)
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		x.routes().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s %s: %d want=%d %s", test.method, test.path, w.Code, test.want, w.Body.String())
		}
	}
}
