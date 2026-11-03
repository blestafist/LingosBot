package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *app {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := initDB(db); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(block)
	x := &app{db: db, aead: aead}
	if _, err := db.Exec(`INSERT INTO sessions VALUES('session','token',?)`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	return x
}

func postSchedule(x *app, values url.Values, cookie bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/schedule", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie {
		r.AddCookie(&http.Cookie{Name: "session", Value: "session"})
	}
	w := httptest.NewRecorder()
	x.saveSchedule(w, r)
	return w
}

func TestScheduleFormAndPersistence(t *testing.T) {
	x := testApp(t)
	v := url.Values{"csrf": {"token"}, "enabled": {"on"}, "time": {"09:05"}, "lessons": {"3"}, "errors": {"12"}}
	if w := postSchedule(x, v, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	v.Set("csrf", "wrong")
	if w := postSchedule(x, v, true); w.Code != http.StatusForbidden {
		t.Fatalf("csrf: %d", w.Code)
	}
	v.Set("csrf", "token")
	for _, tc := range []struct{ field, value string }{{"time", "9:05"}, {"time", "25:00"}, {"lessons", "0"}, {"lessons", "10001"}, {"errors", "101"}, {"errors", "x"}} {
		old := v.Get(tc.field)
		v.Set(tc.field, tc.value)
		if w := postSchedule(x, v, true); w.Code != http.StatusBadRequest {
			t.Errorf("%s=%q: %d", tc.field, tc.value, w.Code)
		}
		v.Set(tc.field, old)
	}
	w := postSchedule(x, v, true)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?schedule_saved=1#schedule" {
		t.Fatalf("save: %d %q", w.Code, w.Header().Get("Location"))
	}
	var seq int
	var name, path string
	if err := x.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := x.db.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	x.db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.db.Close() })
	if err := initDB(x.db); err != nil {
		t.Fatal(err)
	}
	s, err := x.loadSchedule()
	if err != nil || s != (schedule{true, "09:05", 3, 12}) {
		t.Fatalf("saved schedule: %+v, %v", s, err)
	}
	v.Del("enabled")
	if w := postSchedule(x, v, true); w.Code != http.StatusSeeOther {
		t.Fatal(w.Code)
	}
	s, _ = x.loadSchedule()
	if s.Enabled {
		t.Fatal("schedule was not disabled")
	}
}

func storeTestCredentials(t *testing.T, x *app) {
	t.Helper()
	nonce := make([]byte, x.aead.NonceSize())
	enc := x.aead.Seal(nonce, nonce, []byte("secret"), nil)
	if _, err := x.db.Exec(`INSERT INTO credentials VALUES(1,?,?)`, "learner", enc); err != nil {
		t.Fatal(err)
	}
}

func waitWorker(t *testing.T, x *app) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if x.workerMu.TryLock() {
			x.workerMu.Unlock()
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("worker did not stop")
}

func TestScheduledRunAndDeduplication(t *testing.T) {
	x := testApp(t)
	storeTestCredentials(t, x)
	dir := t.TempDir()
	x.worker = filepath.Join(dir, "worker")
	output := filepath.Join(dir, "config")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + args + "'\ncat >> '" + output + "'\n"
	if err := os.WriteFile(x.worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`UPDATE schedule SET enabled=1,time='09:05',lessons=3,errors=12`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 9, 5, 10, 0, time.Local)
	x.runScheduled(now.Add(-time.Minute))
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("ran at wrong time")
	}
	x.runScheduled(now)
	waitWorker(t, x)
	x.runScheduled(now.Add(10 * time.Second))
	waitWorker(t, x)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var got creds
	if err := json.Unmarshal(data, &got); err != nil || got != (creds{"learner", "secret", 3, 12}) {
		t.Fatalf("config %q: %+v, %v", data, got, err)
	}
	argData, err := os.ReadFile(args)
	if err != nil || string(argData) != "--realtime\n--config-json-stdin\n" {
		t.Fatalf("args: %q %v", argData, err)
	}
	if err := initDB(x.db); err != nil {
		t.Fatal(err)
	}
	x.runScheduled(now)
	waitWorker(t, x)
	dataAgain, _ := os.ReadFile(output)
	if string(dataAgain) != string(data) {
		t.Fatal("ran twice after restart")
	}
}

func TestScheduledWorkerMissingCredentialsAndTimeout(t *testing.T) {
	x := testApp(t)
	if err := x.runScheduledWorker(context.Background(), schedule{}); err != sql.ErrNoRows {
		t.Fatalf("missing credentials: %v", err)
	}
	storeTestCredentials(t, x)
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := x.runScheduledWorker(ctx, schedule{}); err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("timeout: %v, context: %v", err, ctx.Err())
	}
}

func TestScheduledSkipWhenBusy(t *testing.T) {
	x := testApp(t)
	if _, err := x.db.Exec(`UPDATE schedule SET enabled=1,time='09:05'`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 9, 5, 0, 0, time.Local)
	x.workerMu.Lock()
	x.runScheduled(now)
	x.workerMu.Unlock()
	var date string
	if err := x.db.QueryRow(`SELECT last_date FROM schedule`).Scan(&date); err != nil || date != "2026-11-03" {
		t.Fatalf("claim: %q, %v", date, err)
	}
}

func TestManualRunRejectsBusyWorker(t *testing.T) {
	x := testApp(t)
	storeTestCredentials(t, x)
	x.workerMu.Lock()
	defer x.workerMu.Unlock()
	r := httptest.NewRequest(http.MethodGet, "/events?mode=run&csrf=token", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "session"})
	w := httptest.NewRecorder()
	x.events(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("busy worker: %d", w.Code)
	}
}
