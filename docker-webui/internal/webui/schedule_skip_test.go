package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func scheduleSkipRequest(x *app, expected int64) *httptest.ResponseRecorder {
	return scheduleRequest(x.skipScheduleAPI, "POST", "/api/schedule/skip", fmt.Sprintf(`{"nextRun":%d}`, expected), true)
}

func TestSkipScheduleRequestValidation(t *testing.T) {
	x := scheduleTestApp(t)
	scheduleSeedDue(t, x, 1, time.Now().Add(time.Hour))
	before, err := x.loadSchedule(1)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"nextRun":%d}`, before.NextRun)
	for _, tc := range []struct {
		name, method, body, csrf, origin string
		authenticated                    bool
		want                             int
	}{
		{"unauthenticated", "POST", body, "csrf", "", false, 401},
		{"unauthenticated GET", "GET", "", "", "", false, 401},
		{"missing CSRF", "POST", body, "", "", true, 403},
		{"wrong CSRF", "POST", body, "wrong", "", true, 403},
		{"cross origin", "POST", body, "csrf", "https://evil.example", true, 403},
		{"GET", "GET", body, "csrf", "", true, 405},
		{"PUT", "PUT", body, "csrf", "", true, 405},
		{"empty", "POST", "", "csrf", "", true, 400},
		{"missing timestamp", "POST", `{}`, "csrf", "", true, 400},
		{"null object", "POST", `null`, "csrf", "", true, 400},
		{"null timestamp", "POST", `{"nextRun":null}`, "csrf", "", true, 400},
		{"zero", "POST", `{"nextRun":0}`, "csrf", "", true, 400},
		{"negative", "POST", `{"nextRun":-1}`, "csrf", "", true, 400},
		{"string", "POST", `{"nextRun":"1"}`, "csrf", "", true, 400},
		{"fraction", "POST", `{"nextRun":1.5}`, "csrf", "", true, 400},
		{"overflow", "POST", `{"nextRun":9223372036854775808}`, "csrf", "", true, 400},
		{"positive int64 is stale", "POST", `{"nextRun":9223372036854775807}`, "csrf", "", true, 409},
		{"array", "POST", `[]`, "csrf", "", true, 400},
		{"unknown field", "POST", strings.TrimSuffix(body, "}") + `,"userId":2}`, "csrf", "", true, 400},
		{"multiple objects", "POST", body + `{}`, "csrf", "", true, 400},
		{"trailing garbage", "POST", body + `x`, "csrf", "", true, 400},
		{"oversized body", "POST", strings.Repeat(" ", 1<<20) + body, "csrf", "", true, 400},
		{"oversized trailing whitespace", "POST", body + strings.Repeat(" ", 1<<20), "csrf", "", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/schedule/skip", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.authenticated {
				r.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("s", 43)})
			}
			w := httptest.NewRecorder()
			x.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 405 && w.Header().Get("Allow") != "POST" {
				t.Fatalf("Allow: %q", w.Header().Get("Allow"))
			}
			after, err := x.loadSchedule(1)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected request changed schedule: %+v, %v", after, err)
			}
		})
	}
}

func TestSkipScheduleConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		args        func(int64) []any
		stale       bool
	}{
		{name: "no saved schedule", query: `DELETE FROM user_schedules WHERE user_id=1`},
		{name: "disabled", query: `UPDATE user_schedules SET enabled=0 WHERE user_id=1`},
		{name: "no pending occurrence", query: `UPDATE user_schedules SET next_run=0 WHERE user_id=1`},
		{name: "stale timestamp", stale: true},
		{name: "active lease", query: `UPDATE user_schedules SET lease_until=? WHERE user_id=1`, args: func(int64) []any { return []any{time.Now().Add(time.Hour).Unix()} }},
		{name: "already started with expired lease", query: `INSERT INTO runs(user_id,mode,class_id,source,status,created_at,scheduled_for)
			VALUES(1,'run','','schedule','running',1,?)`, args: func(due int64) []any { return []any{due} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := scheduleTestApp(t)
			due := time.Now().Add(-time.Hour).Unix()
			scheduleSeedDue(t, x, 1, time.Unix(due, 0))
			if tc.query != "" {
				var args []any
				if tc.args != nil {
					args = tc.args(due)
				}
				if _, err := x.db.Exec(tc.query, args...); err != nil {
					t.Fatal(err)
				}
			}
			before, err := x.loadSchedule(1)
			if err != nil {
				t.Fatal(err)
			}
			expected := due
			if tc.stale {
				expected--
			}
			w := scheduleSkipRequest(x, expected)
			if w.Code != 409 {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			after, err := x.loadSchedule(1)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("conflict changed schedule: %+v, %v", after, err)
			}
			if tc.name == "active lease" {
				var lease int64
				if err := x.db.QueryRow(`SELECT lease_until FROM user_schedules WHERE user_id=1`).Scan(&lease); err != nil || lease <= time.Now().Unix() {
					t.Fatalf("claim was released: %d, %v", lease, err)
				}
			}
			if tc.name == "already started with expired lease" {
				var status string
				if err := x.db.QueryRow(`SELECT status FROM runs`).Scan(&status); err != nil || status != "running" {
					t.Fatalf("started run was changed: %q, %v", status, err)
				}
			}
		})
	}
}

func TestSkipScheduleNextOccurrenceAndHistory(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	friday := time.Date(time.Now().Year()+1, 11, 1, 9, 0, 0, 0, zone)
	for friday.Weekday() != time.Friday {
		friday = friday.AddDate(0, 0, 1)
	}
	for _, tc := range []struct {
		name, cron string
		due, want  time.Time
	}{
		{"weekday and timezone", "", friday, friday.AddDate(0, 0, 3)},
		{"cron overrides time and weekdays", "  */15 8-9 * * MON-FRI  ", friday.Add(45 * time.Minute), friday.AddDate(0, 0, 3).Add(-time.Hour)},
		{"overdue weekdays", "", time.Now().Add(-14 * 24 * time.Hour), time.Time{}},
		{"overdue cron", "*/15 8-9 * * MON-FRI", time.Now().Add(-14 * 24 * time.Hour), time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := scheduleTestApp(t)
			scheduleSeedDue(t, x, 1, tc.due)
			rawDays := "[5, 1]"
			if tc.cron != "" {
				rawDays = "[6, 2]"
			}
			if _, err := x.db.Exec(`INSERT INTO runs(id,user_id,mode,class_id,source,status,summary,created_at)
				VALUES(42,1,'run','','schedule','failed','previous failure',123);
				UPDATE user_schedules SET last_run=123,last_run_id=42,last_status='waiting',last_error='old error',lease_until=1,version=7 WHERE user_id=1`); err != nil {
				t.Fatal(err)
			}
			if _, err := x.db.Exec(`UPDATE user_schedules SET cron=?,days=? WHERE user_id=1`, tc.cron, rawDays); err != nil {
				t.Fatal(err)
			}
			before, err := x.loadSchedule(1)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			w := scheduleSkipRequest(x, before.NextRun)
			end := time.Now()
			if w.Code != 200 {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			var got Schedule
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !tc.want.IsZero() {
				if got.NextRun != tc.want.Unix() {
					t.Fatalf("next %s, want %s", time.Unix(got.NextRun, 0), tc.want)
				}
			} else {
				parsed, err := before.parse()
				if err != nil {
					t.Fatal(err)
				}
				if got.NextRun != parsed.Next(start).Unix() && got.NextRun != parsed.Next(end).Unix() {
					t.Fatalf("overdue slot replayed backlog: %+v", got)
				}
			}
			if got.NextRun <= end.Unix() || got.NextRun <= before.NextRun || !got.Enabled || got.LastStatus != "skipped" || got.LastError != "" || got.LastRun != 123 || got.LastRunID != 42 {
				t.Fatalf("incorrect skip response: %+v", got)
			}
			if got.Time != before.Time || got.Timezone != before.Timezone || got.Cron != before.Cron || !reflect.DeepEqual(got.Days, before.Days) {
				t.Fatalf("skip changed config: %+v", got)
			}
			var days string
			var version, lease, runs int64
			if err := x.db.QueryRow(`SELECT days,version,lease_until,(SELECT count(*) FROM runs) FROM user_schedules WHERE user_id=1`).Scan(&days, &version, &lease, &runs); err != nil || days != rawDays || version != 8 || lease != 0 || runs != 1 {
				t.Fatalf("unexpected storage: days=%q version=%d lease=%d runs=%d, %v", days, version, lease, runs, err)
			}
			get := scheduleRequest(x.scheduleAPI, "GET", "/api/schedule", "", true)
			if get.Code != 200 || get.Body.String() != w.Body.String() {
				t.Fatalf("GET overwrote skipped status: %d: %s", get.Code, get.Body.String())
			}
		})
	}
}

func TestSkipScheduleConcurrentDoubleClick(t *testing.T) {
	x := scheduleTestApp(t)
	due := time.Now().Add(-time.Hour)
	scheduleSeedDue(t, x, 1, due)
	before, err := x.loadSchedule(1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	responses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			responses <- scheduleSkipRequest(x, due.Unix()).Code
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	codes := map[int]int{}
	for code := range responses {
		codes[code]++
	}
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatalf("double click responses: %v", codes)
	}
	after, err := x.loadSchedule(1)
	if err != nil || after.version != before.version+1 {
		t.Fatalf("double click advanced twice: %+v, %v", after, err)
	}
	if w := scheduleSkipRequest(x, due.Unix()); w.Code != 409 {
		t.Fatalf("repeated click: %d: %s", w.Code, w.Body.String())
	}
	final, err := x.loadSchedule(1)
	if err != nil || !reflect.DeepEqual(final, after) {
		t.Fatalf("repeated click changed schedule: %+v, %v", final, err)
	}
}

func TestSkipScheduleUserIsolationAndRunningWorker(t *testing.T) {
	x := scheduleTestApp(t)
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(2,'other@example.com',X'00',0);
		DELETE FROM user_schedules WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Hour)
	scheduleSeedDue(t, x, 2, due)
	other, err := x.loadSchedule(2)
	if err != nil {
		t.Fatal(err)
	}
	if w := scheduleSkipRequest(x, due.Unix()); w.Code != 409 {
		t.Fatalf("skipped another user's schedule: %d: %s", w.Code, w.Body.String())
	}
	after, err := x.loadSchedule(2)
	if err != nil || !reflect.DeepEqual(after, other) {
		t.Fatalf("rejected skip changed another user: %+v, %v", after, err)
	}
	scheduleSeedDue(t, x, 1, due)
	if _, err := x.db.Exec(`INSERT INTO runs(id,user_id,mode,class_id,source,status,created_at,scheduled_for)
		VALUES(7,2,'run','','schedule','running',123,?);
		UPDATE user_schedules SET last_run=123,last_run_id=7,last_status='started' WHERE user_id=2`, due.Unix()); err != nil {
		t.Fatal(err)
	}
	other, err = x.loadSchedule(2)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	x.cancels = map[int64]context.CancelFunc{7: func() { cancelled = true }}
	if w := scheduleSkipRequest(x, due.Unix()); w.Code != 200 {
		t.Fatalf("another user's running slot blocked skip: %d: %s", w.Code, w.Body.String())
	}
	after, err = x.loadSchedule(2)
	if err != nil || !reflect.DeepEqual(after, other) || cancelled {
		t.Fatalf("skip changed another user or stopped worker: %+v, cancelled=%v, %v", after, cancelled, err)
	}
	var status string
	var runs int
	if err := x.db.QueryRow(`SELECT status,(SELECT count(*) FROM runs) FROM runs WHERE id=7`).Scan(&status, &runs); err != nil || status != "running" || runs != 1 {
		t.Fatalf("skip launched/stopped a run: status=%q runs=%d, %v", status, runs, err)
	}
}

func TestSkipScheduleSaveClearsSkippedStatus(t *testing.T) {
	x := scheduleTestApp(t)
	for _, enabled := range []bool{true, false} {
		scheduleSeedDue(t, x, 1, time.Now().Add(-time.Hour))
		s, err := x.loadSchedule(1)
		if err != nil {
			t.Fatal(err)
		}
		if w := scheduleSkipRequest(x, s.NextRun); w.Code != 200 {
			t.Fatalf("skip: %d: %s", w.Code, w.Body.String())
		}
		body := fmt.Sprintf(`{"enabled":%t,"time":"10:00","timezone":"UTC","days":[0]}`, enabled)
		w := scheduleRequest(x.scheduleAPI, "PUT", "/api/schedule", body, true)
		if w.Code != 200 {
			t.Fatalf("save: %d: %s", w.Code, w.Body.String())
		}
		s, err = x.loadSchedule(1)
		if err != nil || s.LastStatus != "" || s.Enabled != enabled || s.Time != "10:00" {
			t.Fatalf("save kept misleading skipped status: %+v, %v", s, err)
		}
	}
}

func TestSkipScheduleInvalidSavedConfigAndStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        int
	}{
		{"no occurrence", `UPDATE user_schedules SET cron='0 0 31 2 *' WHERE user_id=1`, 400},
		{"invalid saved config", `UPDATE user_schedules SET timezone='Not/A_Zone' WHERE user_id=1`, 400},
		{"update failure", `CREATE TRIGGER reject_skip BEFORE UPDATE ON user_schedules BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END`, 500},
		{"read failure", `DROP TABLE user_schedules`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := scheduleTestApp(t)
			due := time.Now().Add(-time.Hour)
			scheduleSeedDue(t, x, 1, due)
			if _, err := x.db.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			before, err := x.loadSchedule(1)
			if err != nil && tc.name != "read failure" {
				t.Fatal(err)
			}
			w := scheduleSkipRequest(x, due.Unix())
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.name != "read failure" {
				after, err := x.loadSchedule(1)
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("failed skip corrupted config: %+v, %v", after, err)
				}
			}
		})
	}
}

func TestSkipScheduleSchedulerAndRestartPersistence(t *testing.T) {
	x := scheduleTestApp(t)
	scheduleCredentials(t, x, 1)
	now := time.Now().Truncate(time.Second)
	due := now.Add(-48 * time.Hour)
	scheduleSeedDue(t, x, 1, due)
	x.workerMu.Lock()
	x.runScheduled(now)
	x.workerMu.Unlock()
	old, err := x.loadSchedule(1)
	if err != nil || old.LastStatus != "waiting" {
		t.Fatalf("expected overdue waiting slot: %+v, %v", old, err)
	}
	if w := scheduleSkipRequest(x, old.NextRun); w.Code != 200 {
		t.Fatalf("skip: %d: %s", w.Code, w.Body.String())
	}
	skipped, err := x.loadSchedule(1)
	if err != nil {
		t.Fatal(err)
	}
	// The scheduler's claim using the snapshot loaded before skip must lose its CAS.
	result, err := x.db.Exec(`UPDATE user_schedules SET lease_until=? WHERE user_id=1 AND enabled=1
		AND next_run=? AND version=? AND lease_until<=?`, now.Add(time.Minute).Unix(), old.NextRun, old.version, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("old scheduler snapshot claimed skipped slot: %d, %v", n, err)
	}
	var sequence int
	var name, path string
	if err := x.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := x.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := initDB(db); err != nil {
		t.Fatal(err)
	}
	if err := initSettingsDB(db); err != nil {
		t.Fatal(err)
	}
	if err := initRunnerDB(db); err != nil {
		t.Fatal(err)
	}
	restarted := &app{db: db, aead: x.aead, worker: x.worker}
	t.Cleanup(restarted.workers.Wait)
	persisted, err := restarted.loadSchedule(1)
	if err != nil || !reflect.DeepEqual(persisted, skipped) {
		t.Fatalf("restart lost skip: %+v, %v", persisted, err)
	}
	restarted.runScheduled(now)
	restarted.runScheduled(time.Unix(skipped.NextRun-1, 0))
	var runs int
	if err := db.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("scheduler started skipped slot: %d, %v", runs, err)
	}
	restarted.runScheduled(time.Unix(skipped.NextRun, 0))
	scheduleWaitRun(t, restarted)
	var scheduledFor int64
	if err := db.QueryRow(`SELECT count(*),scheduled_for FROM runs`).Scan(&runs, &scheduledFor); err != nil || runs != 1 || scheduledFor != skipped.NextRun {
		t.Fatalf("later slot did not run exactly once: runs=%d slot=%d, %v", runs, scheduledFor, err)
	}
	later, err := restarted.loadSchedule(1)
	if err != nil || !later.Enabled || later.NextRun <= skipped.NextRun || later.LastRunID == 0 || later.LastStatus == "skipped" || later.LastError != "" {
		t.Fatalf("later run did not replace skipped status: %+v, %v", later, err)
	}
}
