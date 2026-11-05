package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedAuthDeleteData(t *testing.T, x *app, userID int64) int64 {
	t.Helper()
	password, err := x.encryptCredentials(userID, "lingos secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO user_credentials(user_id,email,password) VALUES(?,'lingos@example.com',?)`, []any{userID, password}},
		{`INSERT INTO user_settings(user_id,config) VALUES(?,'{"lessonCount":7}') ON CONFLICT(user_id) DO UPDATE SET config=excluded.config`, []any{userID}},
		{`INSERT INTO user_schedules(user_id,enabled,time,timezone,days) VALUES(?,1,'12:34','UTC','[1,2]') ON CONFLICT(user_id) DO UPDATE SET enabled=1,time='12:34'`, []any{userID}},
		{`INSERT INTO class_cache(user_id,data) VALUES(?,'[]')`, []any{userID}},
	} {
		if _, err := x.db.Exec(seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.saveVocabulary(userID, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,created_at) VALUES(?,'run','','manual','completed',123)`, userID)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO run_events(run_id,seq,data) VALUES(?,1,'{}'),(?,2,'{}')`, runID, runID); err != nil {
		t.Fatal(err)
	}
	return runID
}

func checkAuthDeleteData(t *testing.T, x *app, userID, runID int64, want int) {
	t.Helper()
	for _, table := range []string{"users", "user_sessions", "user_credentials", "user_settings", "user_schedules", "class_cache", "library_classes", "library_topics", "runs", "run_events"} {
		column, id, count := "user_id", userID, want
		if table == "users" {
			column = "id"
		}
		if table == "run_events" {
			column, id = "run_id", runID
		}
		if table == "library_topics" || table == "run_events" {
			count *= 2
		}
		var got int
		if err := x.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+`=?`, id).Scan(&got); err != nil || got != count {
			t.Errorf("%s user=%d: count=%d, want=%d, err=%v", table, userID, got, count, err)
		}
	}
}

func TestAuthAdminDeleteValidation(t *testing.T) {
	x := integrationApp(t)
	id := authTestUser(t, x, "learner@example.com", false)
	a, ac := authTestSession(t, x, 1)
	b, bc := authTestSession(t, x, id)
	path := fmt.Sprintf("/api/admin/users/%d", id)
	for _, test := range []struct {
		name, path, token, csrf, origin string
		want                            int
	}{
		{"anonymous", path, "", "", "", 401},
		{"non-admin", path, b, bc, "", 403},
		{"missing csrf", path, a, "", "", 403},
		{"wrong csrf", path, a, "wrong", "", 403},
		{"query csrf", path + "?csrf=" + ac, a, "", "", 403},
		{"cross origin", path, a, ac, "https://evil.example", 403},
		{"no id", "/api/admin/users", a, ac, "", 400},
		{"empty id", "/api/admin/users/", a, ac, "", 400},
		{"zero", "/api/admin/users/0", a, ac, "", 400},
		{"negative", "/api/admin/users/-1", a, ac, "", 400},
		{"text", "/api/admin/users/abc", a, ac, "", 400},
		{"overflow", "/api/admin/users/9223372036854775808", a, ac, "", 400},
		{"extra path", path + "/extra", a, ac, "", 400},
		{"missing", "/api/admin/users/99999", a, ac, "", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodDelete, test.path, nil)
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
				t.Fatalf("status=%d, want=%d: %s", w.Code, test.want, w.Body.String())
			}
		})
	}
	var users int
	if err := x.db.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil || users != 2 {
		t.Fatalf("rejected delete changed users: %d, %v", users, err)
	}
}

func TestAuthAdminDeleteLastAdminAndSelf(t *testing.T) {
	x := integrationApp(t)
	a, ac := authTestSession(t, x, 1)
	secondSession, _ := authTestSession(t, x, 1)
	id := authTestUser(t, x, "other-admin@example.com", true)
	if _, err := x.db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	w := authTestRequest(x.adminUsersAPI, "DELETE", "/api/admin/users/1", "", a, ac)
	if w.Code != http.StatusConflict {
		t.Fatalf("disabled admin counted as active: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`UPDATE users SET disabled=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	b, _ := authTestSession(t, x, id)
	w = authTestRequest(x.adminUsersAPI, "DELETE", "/api/admin/users/1", "", a, ac)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"deleted":true}` {
		t.Fatalf("self delete: %d %s", w.Code, w.Body.String())
	}
	for _, token := range []string{a, secondSession} {
		if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != 401 {
			t.Fatalf("deleted user's session survived: %d %s", w.Code, w.Body.String())
		}
	}
	if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", b, ""); w.Code != 200 {
		t.Fatalf("other admin's session revoked: %d %s", w.Code, w.Body.String())
	}
}

func TestAuthAdminDeleteFullCleanup(t *testing.T) {
	for _, target := range []string{"legacy admin", "ordinary user", "disabled user"} {
		t.Run(target, func(t *testing.T) {
			x := integrationApp(t)
			other := authTestUser(t, x, "survivor@example.com", true)
			id := int64(1)
			if target != "legacy admin" {
				id = authTestUser(t, x, "target@example.com", false)
			}
			runID := seedAuthDeleteData(t, x, id)
			otherRun := seedAuthDeleteData(t, x, other)
			token, _ := authTestSession(t, x, id)
			a, ac := authTestSession(t, x, other)
			if target == "disabled user" {
				if _, err := x.db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := x.db.Exec(`INSERT INTO credentials VALUES(1,'legacy@example.com',x'00'); UPDATE schedule SET enabled=1,time='23:45',lessons=99`); err != nil {
				t.Fatal(err)
			}
			w := authTestRequest(x.routes().ServeHTTP, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), "", a, ac)
			if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"deleted":true}` {
				t.Fatalf("delete: %d %s", w.Code, w.Body.String())
			}
			checkAuthDeleteData(t, x, id, runID, 0)
			checkAuthDeleteData(t, x, other, otherRun, 1)
			if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != 401 {
				t.Fatal("deleted user's session survived")
			}
			if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", a, ""); w.Code != 200 {
				t.Fatal("surviving user's session was revoked")
			}
			if email, password, err := x.loadCredentials(other); err != nil || email != "lingos@example.com" || password != "lingos secret" {
				t.Fatalf("other credentials changed: %q %q %v", email, password, err)
			}
			legacyWant := 1
			if id == 1 {
				legacyWant = 0
			}
			for _, table := range []string{"credentials", "schedule"} {
				var count int
				if err := x.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != legacyWant {
					t.Fatalf("legacy %s count=%d, want=%d, err=%v", table, count, legacyWant, err)
				}
			}
			var markers int
			if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM auth_meta WHERE key='legacy_credentials_v1')+(SELECT count(*) FROM settings_meta WHERE key='legacy_schedule_v1')`).Scan(&markers); err != nil || markers != 2 {
				t.Fatalf("migration markers lost: %d, %v", markers, err)
			}
			for _, init := range []func() error{func() error { return initDB(x.db) }, x.bootstrapAdmin, func() error { return initSettingsDB(x.db) }} {
				if err := init(); err != nil {
					t.Fatal(err)
				}
			}
			checkAuthDeleteData(t, x, id, runID, 0)
			checkAuthDeleteData(t, x, other, otherRun, 1)
		})
	}
}

func TestAuthAdminDeletePendingPrecondition(t *testing.T) {
	for _, state := range []string{"pending", "approved", "approved with active run"} {
		t.Run(state, func(t *testing.T) {
			x := integrationApp(t)
			id := authTestUser(t, x, "pending@example.com", false)
			if _, err := x.db.Exec(`UPDATE users SET disabled=1,pending=1 WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			a, ac := authTestSession(t, x, 1)
			path := fmt.Sprintf("/api/admin/users/%d", id)
			if state != "pending" {
				if w := authTestRequest(x.adminUsersAPI, "PATCH", path, `{"approve":true}`, a, ac); w.Code != http.StatusOK {
					t.Fatalf("approve: %d %s", w.Code, w.Body.String())
				}
			}
			runID := seedAuthDeleteData(t, x, id)
			token, _ := authTestSession(t, x, id)
			if state == "approved with active run" {
				if _, err := x.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, runID); err != nil {
					t.Fatal(err)
				}
				x.cancels = map[int64]context.CancelFunc{runID: func() { t.Error("stale rejection canceled a run") }}
			}
			w := authTestRequest(x.routes().ServeHTTP, "DELETE", path+"?pending=1", "", a, ac)
			if state == "pending" {
				if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"deleted":true}` {
					t.Fatalf("reject pending: %d %s", w.Code, w.Body.String())
				}
				checkAuthDeleteData(t, x, id, runID, 0)
				return
			}
			if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != `{"error":"registration request is no longer pending"}` {
				t.Fatalf("stale rejection: %d %s", w.Code, w.Body.String())
			}
			checkAuthDeleteData(t, x, id, runID, 1)
			if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != http.StatusOK {
				t.Fatalf("stale rejection revoked session: %d %s", w.Code, w.Body.String())
			}
			if email, password, err := x.loadCredentials(id); err != nil || email != "lingos@example.com" || password != "lingos secret" {
				t.Fatalf("stale rejection changed credentials: %q %q %v", email, password, err)
			}
			var status string
			wantStatus := "completed"
			if state == "approved with active run" {
				wantStatus = "running"
			}
			if err := x.db.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil || status != wantStatus {
				t.Fatalf("stale rejection changed run: %q %v", status, err)
			}
		})
	}
}

func TestAuthAdminDeleteBusy(t *testing.T) {
	for _, state := range []string{"running record", "terminal live worker", "other live worker"} {
		t.Run(state, func(t *testing.T) {
			x := integrationApp(t)
			id := authTestUser(t, x, "target@example.com", false)
			runID := seedAuthDeleteData(t, x, id)
			a, ac := authTestSession(t, x, 1)
			authTestSession(t, x, id)
			want := 409
			if state == "running record" {
				if _, err := x.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, runID); err != nil {
					t.Fatal(err)
				}
			} else {
				if state == "other live worker" {
					runID = seedAuthDeleteData(t, x, 1)
					want = 200
				}
				x.cancels = map[int64]context.CancelFunc{runID: func() { t.Error("delete cancelled a worker") }}
			}
			w := authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), "", a, ac)
			if w.Code != want {
				t.Fatalf("busy protection: %d, want=%d: %s", w.Code, want, w.Body.String())
			}
			if want == 409 {
				checkAuthDeleteData(t, x, id, runID, 1)
				if state == "terminal live worker" {
					x.runMu.Lock()
					delete(x.cancels, runID)
					x.runMu.Unlock()
					w := authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), "", a, ac)
					if w.Code != 200 {
						t.Fatalf("finished worker still blocked deletion: %d %s", w.Code, w.Body.String())
					}
				}
			}
		})
	}
}

func TestAuthAdminDeleteRollback(t *testing.T) {
	for _, table := range []string{"class_cache", "library_classes", "run_events", "users", "schedule"} {
		t.Run(table, func(t *testing.T) {
			x := integrationApp(t)
			other := authTestUser(t, x, "other-admin@example.com", true)
			runID := seedAuthDeleteData(t, x, 1)
			otherRun := seedAuthDeleteData(t, x, other)
			token, _ := authTestSession(t, x, 1)
			a, ac := authTestSession(t, x, other)
			if _, err := x.db.Exec(`INSERT INTO credentials VALUES(1,'legacy@example.com',x'00'); CREATE TRIGGER reject_delete BEFORE DELETE ON ` + table + ` BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
				t.Fatal(err)
			}
			var before, after int64
			if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter WHERE id=1`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			w := authTestRequest(x.adminUsersAPI, "DELETE", "/api/admin/users/1", "", a, ac)
			if w.Code != 500 || strings.Contains(w.Body.String(), "synthetic failure") {
				t.Fatalf("failed delete: %d %s", w.Code, w.Body.String())
			}
			checkAuthDeleteData(t, x, 1, runID, 1)
			checkAuthDeleteData(t, x, other, otherRun, 1)
			if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter WHERE id=1`).Scan(&after); err != nil || before != after {
				t.Fatalf("failed delete changed watermark: before=%d after=%d err=%v", before, after, err)
			}
			var legacy int
			if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM credentials)+(SELECT count(*) FROM schedule)`).Scan(&legacy); err != nil || legacy != 2 {
				t.Fatalf("failed delete lost legacy data: %d, %v", legacy, err)
			}
			if w := authTestRequest(x.accountAPI, "GET", "/api/account", "", token, ""); w.Code != 200 {
				t.Fatal("failed delete revoked session")
			}
		})
	}
}

func TestAuthAdminDeleteDoesNotReuseIDs(t *testing.T) {
	x := integrationApp(t)
	a, ac := authTestSession(t, x, 1)
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(100,'legacy@example.com',x'00',0); DROP TABLE user_id_counter`); err != nil {
		t.Fatal(err)
	}
	if err := initDB(x.db); err != nil {
		t.Fatal(err)
	}
	deleteUser := func(id int64) {
		t.Helper()
		w := authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), "", a, ac)
		if w.Code != 200 {
			t.Fatalf("delete %d: %d %s", id, w.Code, w.Body.String())
		}
	}
	createUser := func(email string, want int64) {
		t.Helper()
		body := fmt.Sprintf(`{"email":%q,"password":"another secure password"}`, email)
		w := authTestRequest(x.adminUsersAPI, "POST", "/api/admin/users", body, a, ac)
		var u User
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &u) != nil || u.ID != want {
			t.Fatalf("create user: %d %s, want id=%d", w.Code, w.Body.String(), want)
		}
	}
	deleteUser(100)
	createUser("first@example.com", 101)
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(500,'imported@example.com',x'00',0)`); err != nil {
		t.Fatal(err)
	}
	createUser("second@example.com", 501)
	deleteUser(501)
	deleteUser(500)
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
	x.db = db
	if err := initDB(db); err != nil {
		t.Fatal(err)
	}
	createUser("third@example.com", 502)
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(900,'imported-again@example.com',x'00',0)`); err != nil {
		t.Fatal(err)
	}
	deleteUser(900)
	createUser("fourth@example.com", 901)
}

func TestAuthAdminDeleteConcurrentAdminChanges(t *testing.T) {
	for _, operation := range []string{"delete", "demote", "demote actor"} {
		t.Run(operation, func(t *testing.T) {
			x := integrationApp(t)
			id := authTestUser(t, x, "other-admin@example.com", true)
			a, ac := authTestSession(t, x, 1)
			b, bc := authTestSession(t, x, id)
			firstTarget, secondTarget := int64(1), id
			method, body, rejected := "DELETE", "", 409
			if operation != "delete" {
				method, body = "PATCH", `{"admin":false}`
			}
			if operation == "demote actor" {
				firstTarget, secondTarget, rejected = id, 1, 403
			}
			start := make(chan struct{})
			responses := make(chan *httptest.ResponseRecorder, 2)
			go func() {
				<-start
				responses <- authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", firstTarget), "", a, ac)
			}()
			go func() {
				<-start
				responses <- authTestRequest(x.adminUsersAPI, method, fmt.Sprintf("/api/admin/users/%d", secondTarget), body, b, bc)
			}()
			close(start)
			statuses := map[int]int{}
			for range 2 {
				w := <-responses
				statuses[w.Code]++
			}
			var active int
			if err := x.db.QueryRow(`SELECT count(*) FROM users WHERE admin=1 AND disabled=0`).Scan(&active); err != nil || active != 1 || statuses[200] != 1 {
				t.Fatalf("concurrent changes: active=%d statuses=%v err=%v", active, statuses, err)
			}
			if statuses[rejected] != 1 && !(operation == "demote actor" && statuses[401] == 1) {
				t.Fatalf("unexpected concurrent rejection: %v", statuses)
			}
		})
	}
}

func TestAuthAdminDeleteConcurrentLaunch(t *testing.T) {
	x := integrationApp(t)
	id := authTestUser(t, x, "target@example.com", false)
	seedAuthDeleteData(t, x, id)
	a, ac := authTestSession(t, x, 1)
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	x.runMu.Lock()
	launched := make(chan error, 1)
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		_, err := x.startRun(id, "run", "", "manual")
		launched <- err
	}()
	go func() {
		deleted <- authTestRequest(x.adminUsersAPI, "DELETE", fmt.Sprintf("/api/admin/users/%d", id), "", a, ac)
	}()
	x.runMu.Unlock()
	err := <-launched
	w := <-deleted
	if !(w.Code == 200 && err != nil || w.Code == 409 && err == nil) {
		t.Fatalf("delete/start race: delete=%d %s launch=%v", w.Code, w.Body.String(), err)
	}
	x.runMu.Lock()
	for _, cancel := range x.cancels {
		cancel()
	}
	x.runMu.Unlock()
	x.workers.Wait()
	var users, runs int
	if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM users WHERE id=?),(SELECT count(*) FROM runs WHERE user_id=?)`, id, id).Scan(&users, &runs); err != nil {
		t.Fatal(err)
	}
	if w.Code == 200 && (users != 0 || runs != 0) || w.Code == 409 && (users != 1 || runs != 2) {
		t.Fatalf("delete/start left inconsistent data: status=%d users=%d runs=%d", w.Code, users, runs)
	}
}
