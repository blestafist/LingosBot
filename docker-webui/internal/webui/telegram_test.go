package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func telegramAPITestApp(t *testing.T) *app {
	t.Helper()
	x := integrationApp(t)
	if err := initTelegramDB(x.db); err != nil {
		t.Fatal(err)
	}
	x.telegram = newTelegramBot("123456:synthetic-test-only")
	x.telegram.username = "LingosTestBot"
	return x
}

func telegramTestCode(t *testing.T, x *app, userID int64) string {
	t.Helper()
	token, csrf := authTestSession(t, x, userID)
	w := authTestRequest(x.telegramLinkAPI, "POST", "/api/telegram/link", "", token, csrf)
	var response struct {
		Code    string `json:"code"`
		Expires int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 201 || len(response.Code) != 43 || response.Expires <= time.Now().Unix() {
		t.Fatalf("code: status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	return response.Code
}

func telegramTestBind(t *testing.T, x *app, userID, chatID int64) {
	t.Helper()
	code := telegramTestCode(t, x, userID)
	if ok, err := x.linkTelegram(context.Background(), code, chatID, "learner", "Learner"); err != nil || !ok {
		t.Fatalf("link: %v %v", ok, err)
	}
}

func TestTelegramAPIBoundaries(t *testing.T) {
	x := telegramAPITestApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, path := range []string{"/api/telegram", "/api/telegram/link"} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
			w := authTestRequest(x.routes().ServeHTTP, method, path, `{}`, "", "")
			if w.Code != 401 {
				t.Fatalf("anonymous %s %s: %d", method, path, w.Code)
			}
			if method == "GET" {
				continue
			}
			w = authTestRequest(x.routes().ServeHTTP, method, path, `{}`, token, "")
			if w.Code != 403 {
				t.Fatalf("no CSRF %s %s: %d", method, path, w.Code)
			}
			r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
			r.Header.Set("X-CSRF-Token", csrf)
			r.Header.Set("Origin", "https://evil.example")
			out := httptest.NewRecorder()
			x.routes().ServeHTTP(out, r)
			if out.Code != 403 {
				t.Fatalf("cross origin: %d", out.Code)
			}
		}
	}
	for _, body := range []string{`{}`, `{"notifications":null}`, `{"notifications":"true"}`, `{"notifications":true,"user_id":2}`} {
		if w := authTestRequest(x.telegramAPI, "PATCH", "/api/telegram", body, token, csrf); w.Code != 400 {
			t.Fatalf("invalid patch %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := authTestRequest(x.secure(x.routes()).ServeHTTP, "POST", "/api/telegram/link", "", token, csrf)
	if w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("link response must not be cached: %d %v", w.Code, w.Header())
	}
	if w := authTestRequest(x.telegramAPI, "PATCH", "/api/telegram", `{"notifications":true}`, token, csrf); w.Code != 409 {
		t.Fatalf("unlinked patch: %d", w.Code)
	}
	x.telegram = nil
	w = authTestRequest(x.telegramAPI, "GET", "/api/telegram", "", token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("disabled: %d %s", w.Code, w.Body.String())
	}
	if w = authTestRequest(x.telegramLinkAPI, "POST", "/api/telegram/link", "", token, csrf); w.Code != 503 {
		t.Fatalf("disabled issue: %d", w.Code)
	}
}

func TestTelegramCodeLifecycleAndIsolation(t *testing.T) {
	x := telegramAPITestApp(t)
	other := authTestUser(t, x, "other@example.test", false)
	old := telegramTestCode(t, x, 1)
	code := telegramTestCode(t, x, 1)
	var stored string
	if err := x.db.QueryRow(`SELECT token_hash FROM telegram_tokens WHERE user_id=1`).Scan(&stored); err != nil || stored != tokenHash(code) || stored == code {
		t.Fatalf("token hash: %q %v", stored, err)
	}
	if ok, err := x.linkTelegram(context.Background(), old, 100, "", ""); err != nil || ok {
		t.Fatalf("rotated code accepted: %v %v", ok, err)
	}
	for _, candidate := range []string{"", "bad", strings.Repeat("!", 43), code + "a"} {
		if ok, err := x.linkTelegram(context.Background(), candidate, 100, "", ""); err != nil || ok {
			t.Fatalf("invalid code accepted: %v %v", ok, err)
		}
	}
	if ok, err := x.linkTelegram(context.Background(), code, -100, "", ""); err != nil || ok {
		t.Fatalf("group chat accepted")
	}
	if ok, err := x.linkTelegram(context.Background(), code, 100, "learner", "<script>"); err != nil || !ok {
		t.Fatalf("link: %v %v", ok, err)
	}
	if ok, err := x.linkTelegram(context.Background(), code, 101, "", ""); err != nil || ok {
		t.Fatalf("replayed code accepted")
	}
	token, csrf := authTestSession(t, x, 1)
	w := authTestRequest(x.telegramAPI, "GET", "/api/telegram", "", token, "")
	var state telegramStatus
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || !state.Linked || state.ChatID != "100" || state.Notifications || state.CodeExpiresAt != 0 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), stored) || strings.Contains(w.Body.String(), x.telegram.token) {
		t.Fatal("secret in GET")
	}
	if w = authTestRequest(x.telegramLinkAPI, "POST", "/api/telegram/link", "", token, csrf); w.Code != 409 {
		t.Fatalf("already linked: %d", w.Code)
	}
	otherCode := telegramTestCode(t, x, other)
	if ok, err := x.linkTelegram(context.Background(), otherCode, 100, "", ""); err != nil || ok {
		t.Fatal("chat stolen")
	}
	otherToken, otherCSRF := authTestSession(t, x, other)
	w = authTestRequest(x.telegramAPI, "GET", "/api/telegram", "", otherToken, "")
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Linked || state.ChatID != "" {
		t.Fatalf("foreign link exposed: %s", w.Body.String())
	}
	if w = authTestRequest(x.telegramAPI, "DELETE", "/api/telegram", "", otherToken, otherCSRF); w.Code != 200 {
		t.Fatalf("cancel: %d", w.Code)
	}
	if ok, err := x.linkTelegram(context.Background(), otherCode, 200, "", ""); err != nil || ok {
		t.Fatal("cancelled code accepted")
	}
	if w = authTestRequest(x.telegramAPI, "PATCH", "/api/telegram", `{"notifications":true}`, token, csrf); w.Code != 200 {
		t.Fatalf("enable: %d", w.Code)
	}
	x.telegram = nil
	if w = authTestRequest(x.telegramAPI, "DELETE", "/api/telegram", "", token, csrf); w.Code != 200 {
		t.Fatalf("unlink without bot: %d", w.Code)
	}
}

func TestTelegramExpiredDisabledAndResetCodes(t *testing.T) {
	x := telegramAPITestApp(t)
	userID := authTestUser(t, x, "learner@example.test", false)
	for _, query := range []string{
		`UPDATE telegram_tokens SET expires=0 WHERE user_id=?`,
		`UPDATE users SET disabled=1 WHERE id=?`,
		`UPDATE users SET password_hash=x'00' WHERE id=?`,
	} {
		code := telegramTestCode(t, x, userID)
		if _, err := x.db.Exec(query, userID); err != nil {
			t.Fatal(err)
		}
		if ok, err := x.linkTelegram(context.Background(), code, 100, "", ""); err != nil || ok {
			t.Fatalf("invalidated code accepted for %s", query)
		}
		if _, err := x.db.Exec(`UPDATE users SET disabled=0 WHERE id=?`, userID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTelegramLinkConcurrentAndRollback(t *testing.T) {
	x := telegramAPITestApp(t)
	code := telegramTestCode(t, x, 1)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, id := range []int64{100, 200} {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			ok, err := x.linkTelegram(context.Background(), code, chatID, "", "")
			if err != nil {
				t.Error(err)
			}
			results <- ok
		}(id)
	}
	wg.Wait()
	close(results)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("code consumed %d times", count)
	}
	if err := x.unlinkTelegram(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	code = telegramTestCode(t, x, 1)
	if _, err := x.db.Exec(`CREATE TRIGGER fail_telegram BEFORE DELETE ON telegram_tokens BEGIN SELECT RAISE(ABORT,'synthetic failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if ok, err := x.linkTelegram(context.Background(), code, 300, "", ""); err == nil || ok {
		t.Fatal("partial link committed")
	}
	var links int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_links`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("rollback: links=%d err=%v", links, err)
	}
}

func telegramTestRun(t *testing.T, x *app, userID int64, mode, status string) int64 {
	t.Helper()
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,created_at) VALUES(?,?,'','manual','running',?)`, userID, mode, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	x.finishRun(id, status, "Synthetic result")
	return id
}

func TestTelegramOutboxOptInRecoveryAndCleanup(t *testing.T) {
	x := telegramAPITestApp(t)
	userID := authTestUser(t, x, "learner@example.test", false)
	telegramTestBind(t, x, userID, 100)
	telegramTestRun(t, x, userID, "run", "completed")
	var count int
	check := func(want int) {
		t.Helper()
		if err := x.db.QueryRow(`SELECT count(*) FROM telegram_outbox`).Scan(&count); err != nil || count != want {
			t.Fatalf("queue=%d want=%d err=%v", count, want, err)
		}
	}
	check(0)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1 WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"completed", "failed", "cancelled", "interrupted"} {
		id := telegramTestRun(t, x, userID, "run", status)
		x.finishRun(id, status, "Same terminal result")
	}
	check(4)
	telegramTestRun(t, x, 1, "scrape_all", "completed")
	check(4)
	if err := initTelegramDB(x.db); err != nil {
		t.Fatal(err)
	}
	check(4)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=0 WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1 WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	telegramTestRun(t, x, userID, "classes", "failed")
	check(0)
	telegramTestRun(t, x, userID, "run", "failed")
	check(1)
	if _, err := x.db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := x.db.Exec(`UPDATE users SET disabled=0 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	telegramTestRun(t, x, userID, "scrape_all", "completed")
	check(0)
	telegramTestRun(t, x, userID, "run", "completed")
	check(1)
	if err := x.unlinkTelegramChat(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	check(0)
}

func TestTelegramOutboxLessonOnlyMigration(t *testing.T) {
	x := telegramAPITestApp(t)
	telegramTestBind(t, x, 1, 100)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1;
		DROP TRIGGER telegram_run_finished;
		CREATE TRIGGER telegram_run_finished AFTER UPDATE OF status ON runs
		WHEN OLD.status='running' AND NEW.status IN ('completed','failed','cancelled','interrupted')
		BEGIN INSERT OR IGNORE INTO telegram_outbox(run_id,chat_id)
		SELECT NEW.id,l.chat_id FROM telegram_links l JOIN users u ON u.id=l.user_id
		WHERE l.user_id=NEW.user_id AND l.notifications=1 AND u.disabled=0; END;`); err != nil {
		t.Fatal(err)
	}
	lesson := telegramTestRun(t, x, 1, "run", "completed")
	for _, mode := range []string{"classes", "scrape", "scrape_all"} {
		telegramTestRun(t, x, 1, mode, "failed")
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_outbox`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("legacy queue not seeded: %d %v", count, err)
	}
	for range 2 {
		if err := initTelegramDB(x.db); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"classes", "scrape", "scrape_all"} {
			telegramTestRun(t, x, 1, mode, "completed")
		}
		var runID int64
		var enabled bool
		if err := x.db.QueryRow(`SELECT count(*),min(run_id),(SELECT notifications FROM telegram_links WHERE user_id=1) FROM telegram_outbox`).Scan(&count, &runID, &enabled); err != nil || count != 1 || runID != lesson || !enabled {
			t.Fatalf("migration lost lesson, kept non-lessons or changed opt-in: %d %d %v %v", count, runID, enabled, err)
		}
	}
}

func TestTelegramAdminDeleteCascades(t *testing.T) {
	x := telegramAPITestApp(t)
	userID := authTestUser(t, x, "learner@example.test", false)
	telegramTestBind(t, x, userID, 100)
	if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1 WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	telegramTestRun(t, x, userID, "run", "completed")
	token, csrf := authTestSession(t, x, 1)
	w := authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", userID), "", token, csrf)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	for _, table := range []string{"telegram_links", "telegram_tokens", "telegram_outbox"} {
		var count int
		if err := x.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: count=%d err=%v", table, count, err)
		}
	}
}
