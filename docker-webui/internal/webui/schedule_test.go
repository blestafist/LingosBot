package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func scheduleTestApp(t *testing.T) *app {
	t.Helper()
	t.Setenv("TZ", "UTC")
	t.Setenv("WORKER_BASE_URL", "https://lingos.pl")
	t.Setenv("WORKER_DASHBOARD_URL", "/student/dashboard")
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
	x := &app{db: db, aead: aead, admin: "admin@example.com", password: "test-password-long"}
	t.Cleanup(x.workers.Wait)
	if err := x.bootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := initSettingsDB(db); err != nil {
		t.Fatal(err)
	}
	if err := initRunnerDB(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_sessions(token,csrf,user_id,expires) VALUES(?,'csrf',1,?)`, tokenHash(strings.Repeat("s", 43)), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"success\":true,\"lessons_completed\":1}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return x
}

func scheduleRequest(handler http.HandlerFunc, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if authenticated {
		r.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("s", 43)})
		r.Header.Set("X-CSRF-Token", "csrf")
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func scheduleSeedDue(t *testing.T, x *app, userID int64, due time.Time) {
	t.Helper()
	_, err := x.db.Exec(`INSERT INTO user_schedules(user_id,enabled,time,timezone,days,next_run) VALUES(?,1,'09:00','Europe/Warsaw','[1,2,3,4,5]',?)
		ON CONFLICT(user_id) DO UPDATE SET enabled=1,time=excluded.time,timezone=excluded.timezone,days=excluded.days,cron='',next_run=excluded.next_run,lease_until=0`, userID, due.Unix())
	if err != nil {
		t.Fatal(err)
	}
}

func scheduleCredentials(t *testing.T, x *app, userID int64) {
	t.Helper()
	encrypted, err := x.encryptCredentials(userID, "lingos-password")
	if err == nil {
		_, err = x.db.Exec(`INSERT INTO user_credentials(user_id,email,password) VALUES(?,'student@example.com',?) ON CONFLICT(user_id) DO UPDATE SET password=excluded.password`, userID, encrypted)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func scheduleWaitRun(t *testing.T, x *app) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var running int
		if err := x.db.QueryRow(`SELECT count(*) FROM runs WHERE status IN ('queued','running')`).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("worker did not finish")
}

func TestScheduleTimezoneDSTAndCron(t *testing.T) {
	s := Schedule{Time: "02:30", Timezone: "Europe/Warsaw", Days: []int{0, 1, 2, 3, 4, 5, 6}}
	parsed, err := s.parse()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ after, want string }{
		{"2026-03-28T02:00:00Z", "2026-03-30T00:30:00Z"}, // nonexistent spring hour is skipped
		{"2026-10-25T00:30:00Z", "2026-10-25T01:30:00Z"}, // both real fall-back instants are supported
	} {
		after, _ := time.Parse(time.RFC3339, tc.after)
		if got := parsed.Next(after).UTC().Format(time.RFC3339); got != tc.want {
			t.Errorf("after %s: got %s, want %s", tc.after, got, tc.want)
		}
	}
	s.Time, s.Days, s.Cron = "09:00", []int{1, 2, 3, 4, 5}, "*/15 8-9 * * MON-FRI"
	parsed, err = s.parse()
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC) // Friday after 09:45 Warsaw
	if got := parsed.Next(after).UTC().Format(time.RFC3339); got != "2026-11-09T07:00:00Z" {
		t.Fatal(got)
	}
	s.Cron = ""
	parsed, _ = s.parse()
	if got := parsed.Next(after).UTC().Format(time.RFC3339); got != "2026-11-09T08:00:00Z" {
		t.Fatal(got)
	}
}

func TestScheduleValidation(t *testing.T) {
	for _, change := range []func(*Schedule){
		func(s *Schedule) { s.Time = "9:00" }, func(s *Schedule) { s.Time = "25:00" },
		func(s *Schedule) { s.Timezone = "" }, func(s *Schedule) { s.Timezone = "Local" },
		func(s *Schedule) { s.Timezone = "Not/A_Zone" }, func(s *Schedule) { s.Days = nil },
		func(s *Schedule) { s.Days = []int{-1} }, func(s *Schedule) { s.Days = []int{7} },
		func(s *Schedule) { s.Days = []int{1, 1} }, func(s *Schedule) { s.Cron = "@daily" },
		func(s *Schedule) { s.Cron = "0 0 9 * * *" }, func(s *Schedule) { s.Cron = "61 9 * * *" },
		func(s *Schedule) { s.Cron = "CRON_TZ=UTC 0 9 * * *" },
	} {
		s := defaultSchedule()
		change(&s)
		if _, err := s.parse(); err == nil {
			t.Errorf("accepted invalid schedule: %+v", s)
		}
	}
}

func TestScheduleAPI(t *testing.T) {
	x := scheduleTestApp(t)
	if w := scheduleRequest(x.scheduleAPI, "GET", "/api/schedule", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	body := `{"enabled":true,"time":"09:00","timezone":"Europe/Warsaw","days":[5,1],"cron":""}`
	w := scheduleRequest(x.scheduleAPI, "PUT", "/api/schedule", body, true)
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var got Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.NextRun <= time.Now().Unix() || got.Timezone != "Europe/Warsaw" || got.Days[0] != 1 {
		t.Fatalf("%+v, %v", got, err)
	}
	if w := scheduleRequest(x.scheduleAPI, "PUT", "/api/schedule", strings.Replace(body, `"cron":""`, `"cron":"0 0 31 2 *"`, 1), true); w.Code != 400 {
		t.Fatalf("impossible date: %d: %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`UPDATE user_schedules SET lease_until=? WHERE user_id=1`, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if w := scheduleRequest(x.scheduleAPI, "PUT", "/api/schedule", body, true); w.Code != 409 {
		t.Fatalf("active claim: %d", w.Code)
	}
	if _, err := x.db.Exec(`UPDATE user_schedules SET lease_until=0,last_status='waiting',last_error='old error' WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	w = scheduleRequest(x.scheduleAPI, "PUT", "/api/schedule", `{"enabled":false,"time":"10:00","timezone":"UTC","days":[0]}`, true)
	if w.Code != 200 {
		t.Fatalf("disable: %d: %s", w.Code, w.Body.String())
	}
	s, err := x.loadSchedule(1)
	if err != nil || s.Enabled || s.NextRun != 0 || s.Time != "10:00" || s.LastStatus == "waiting" || s.LastError != "" {
		t.Fatalf("disabled: %+v, %v", s, err)
	}
}

func TestScheduleBusyAndMissingCredentialsRetry(t *testing.T) {
	x := scheduleTestApp(t)
	now := time.Now().Truncate(time.Second)
	due := now.Add(-48 * time.Hour)
	scheduleSeedDue(t, x, 1, due)
	x.runScheduled(now)
	s, err := x.loadSchedule(1)
	if err != nil || s.NextRun != due.Unix() || s.LastStatus != "waiting" || !strings.Contains(s.LastError, "Save Lingos credentials") {
		t.Fatalf("missing credentials consumed slot: %+v, %v", s, err)
	}
	scheduleCredentials(t, x, 1)
	x.workerMu.Lock()
	x.runScheduled(now)
	x.workerMu.Unlock()
	s, err = x.loadSchedule(1)
	if err != nil || s.NextRun != due.Unix() || s.LastStatus != "waiting" || !strings.Contains(strings.ToLower(s.LastError), "busy") {
		t.Fatalf("busy worker consumed slot: %+v, %v", s, err)
	}
	x.runScheduled(now)
	scheduleWaitRun(t, x)
	s, err = x.loadSchedule(1)
	if err != nil || s.NextRun <= now.Unix() || s.LastRunID == 0 || s.LastError != "" {
		t.Fatalf("retry failed: %+v, %v", s, err)
	}
	x.runScheduled(now.Add(5 * time.Second))
	var runs int
	if err := x.db.QueryRow(`SELECT count(*) FROM runs WHERE user_id=1`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("catch-up replayed %d runs: %v", runs, err)
	}
}

func TestScheduleLeaseRestartAndDurableDeduplication(t *testing.T) {
	x := scheduleTestApp(t)
	scheduleCredentials(t, x, 1)
	now := time.Now().Truncate(time.Second)
	due := now.Add(-time.Hour)
	scheduleSeedDue(t, x, 1, due)
	if _, err := x.db.Exec(`UPDATE user_schedules SET lease_until=? WHERE user_id=1`, now.Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	x.runScheduled(now)
	var runs int
	if err := x.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("claimed schedule ran: %d, %v", runs, err)
	}
	// A different process can recover the expired lease using the same database.
	restarted := &app{db: x.db, aead: x.aead, worker: x.worker}
	restarted.runScheduled(now.Add(61 * time.Second))
	scheduleWaitRun(t, restarted)
	s, err := x.loadSchedule(1)
	if err != nil || s.LastRunID == 0 {
		t.Fatalf("expired lease not recovered: %+v, %v", s, err)
	}
	originalID := s.LastRunID
	originalTime := s.LastRun
	if _, err := x.db.Exec(`UPDATE runs SET status='interrupted',summary='Server stopped; check Lingos before retrying.' WHERE id=?`, originalID); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the durable run INSERT but before advancing next_run.
	scheduleSeedDue(t, x, 1, due)
	restarted.runScheduled(now.Add(62 * time.Second))
	if err := x.db.QueryRow(`SELECT count(*) FROM runs WHERE user_id=1 AND scheduled_for=?`, due.Unix()).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("durable slot ran twice: %d, %v", runs, err)
	}
	s, err = x.loadSchedule(1)
	if err != nil || s.LastRunID != originalID || s.LastRun != originalTime || s.NextRun <= now.Unix() || s.LastStatus != "interrupted" || s.LastError == "" {
		t.Fatalf("deduplicated run did not advance schedule: %+v, %v", s, err)
	}
}

func TestScheduleDatabaseLockAndMissingBinaryRetry(t *testing.T) {
	x := scheduleTestApp(t)
	scheduleCredentials(t, x, 1)
	now := time.Now().Truncate(time.Second)
	due := now.Add(-time.Hour)
	scheduleSeedDue(t, x, 1, due)
	var sequence int
	var name, path string
	if err := x.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	tx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE user_schedules SET next_run=next_run WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	x.runScheduled(now)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	s, err := x.loadSchedule(1)
	if err != nil || s.NextRun != due.Unix() || s.LastRunID != 0 {
		t.Fatalf("storage lock consumed slot: %+v, %v", s, err)
	}
	worker := x.worker
	x.worker = filepath.Join(t.TempDir(), "missing-worker")
	x.runScheduled(now)
	s, err = x.loadSchedule(1)
	if err != nil || s.NextRun != due.Unix() || s.LastRunID != 0 || s.LastStatus != "waiting" {
		t.Fatalf("unavailable binary consumed slot: %+v, %v", s, err)
	}
	x.worker = worker
	x.runScheduled(now)
	scheduleWaitRun(t, x)
	s, err = x.loadSchedule(1)
	if err != nil || s.LastRunID == 0 || s.NextRun <= now.Unix() {
		t.Fatalf("retry failed: %+v, %v", s, err)
	}
}

func TestScheduleConcurrentPollsAndDisabledUsers(t *testing.T) {
	x := scheduleTestApp(t)
	scheduleCredentials(t, x, 1)
	now := time.Now().Truncate(time.Second)
	scheduleSeedDue(t, x, 1, now.Add(-time.Hour))
	other := &app{db: x.db, aead: x.aead, worker: x.worker}
	var wg sync.WaitGroup
	for _, instance := range []*app{x, other} {
		wg.Add(1)
		go func(x *app) { defer wg.Done(); x.runScheduled(now) }(instance)
	}
	wg.Wait()
	scheduleWaitRun(t, x)
	var runs int
	if err := x.db.QueryRow(`SELECT count(*) FROM runs WHERE user_id=1`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("concurrent polls: %d runs, %v", runs, err)
	}
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,disabled,created_at) VALUES(2,'disabled@example.com',X'00',1,0)`); err != nil {
		t.Fatal(err)
	}
	scheduleSeedDue(t, x, 2, now.Add(-time.Hour))
	scheduleCredentials(t, x, 2)
	x.runScheduled(now)
	if err := x.db.QueryRow(`SELECT count(*) FROM runs WHERE user_id=2`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("disabled user ran: %d, %v", runs, err)
	}
}

func TestScheduleLoopCancellation(t *testing.T) {
	x := scheduleTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { x.scheduleLoop(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler ignored cancellation")
	}
}
