package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunnerDashboardOneOffLessonCount(t *testing.T) {
	t.Setenv("WORKER_BASE_URL", "https://worker.example.com")
	t.Setenv("WORKER_DASHBOARD_URL", "/student/custom")
	t.Setenv("WORKER_MAX_RUN_SECONDS", "10")
	t.Setenv("LINGOS_LESSON_COUNT", "999")
	t.Setenv("ADMIN_PASSWORD", "server-secret")
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	token, csrf := authTestSession(t, x, 1)
	s := defaultSettings()
	s.BaseURL, s.StudentDashboardURL = "https://legacy.example.com", "/legacy"
	s.LessonCount = 7
	s.ClassLessonCounts = map[string]int{"Skip": 0, "Custom": 3}
	s.LessonPromptSafetyCap, s.ErrorsPer100Words = 17, 12
	s.AnswerDelayMinMs, s.AnswerDelayMaxMs = 40, 80
	s.RequestTimeoutSeconds, s.MaxRunSeconds = 25, 100
	saved, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO user_settings(user_id,config) VALUES(1,?) ON CONFLICT(user_id) DO UPDATE SET config=excluded.config`, string(saved)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "config")
	x.worker = filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncat > '" + capture + "'\ntest -z \"$LINGOS_LESSON_COUNT$ADMIN_PASSWORD$ENCRYPTION_KEY\" || exit 1\nprintf '%s\\n' '{\"type\":\"result\",\"success\":true,\"lessons_completed\":2}'\n"
	if err := os.WriteFile(x.worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	check := func(id int64, count int) {
		t.Helper()
		x.workers.Wait()
		run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
		if err != nil || run.Status != "completed" {
			t.Fatalf("run: %+v %v", run, err)
		}
		raw, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			WorkerSettings
			Credentials map[string]string `json:"credentials"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		want := s
		want.BaseURL, want.StudentDashboardURL = "https://worker.example.com", "/student/custom"
		want.LessonCount, want.MaxRunSeconds = count, 10
		if !reflect.DeepEqual(config.WorkerSettings, want) || !reflect.DeepEqual(config.Credentials, map[string]string{"email": "learner@example.com", "password": "worker-secret"}) {
			t.Fatalf("one-off changed more than the count/deployment policy: %+v", config.WorkerSettings)
		}
		var stored string
		if err := x.db.QueryRow(`SELECT config FROM user_settings WHERE user_id=1`).Scan(&stored); err != nil || stored != string(saved) {
			t.Fatalf("saved config changed: %s %v", stored, err)
		}
	}
	for _, tc := range []struct {
		body  string
		count int
	}{
		{`{"mode":"run","lesson_count":1}`, 1},
		{`{"mode":"run","lesson_count":10000}`, 10000},
		{`{"mode":"run"}`, s.LessonCount},
	} {
		w := authTestRequest(x.runsAPI, "POST", "/api/runs", tc.body, token, csrf)
		var response struct {
			Run RunRecord `json:"run"`
		}
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Run.Source != "manual" {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "worker-secret") {
			t.Fatal("start response leaked credentials")
		}
		check(response.Run.ID, tc.count)
	}
	id, err := x.startScheduledRun(1, 12345)
	if err != nil {
		t.Fatal(err)
	}
	check(id, s.LessonCount)
}

func TestRunnerDashboardLessonCountValidation(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, value := range []string{"0", "-1", "10001", "999999999999999999999999999", "null", "true", `"1"`, "1.5", "1e3", "[]", "{}"} {
		body := `{"mode":"run","lesson_count":` + value + `}`
		w := authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "lesson_count") {
			t.Errorf("lesson_count %s: %d %s", value, w.Code, w.Body.String())
		}
	}
	for _, mode := range []string{"classes", "scrape"} {
		for _, value := range []string{"1", "0", "null"} {
			id := ""
			if mode == "scrape" {
				id = "42"
			}
			body := fmt.Sprintf(`{"mode":%q,"class_id":%q,"lesson_count":%s}`, mode, id, value)
			w := authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "lesson_count") {
				t.Errorf("%s lesson_count %s: %d %s", mode, value, w.Code, w.Body.String())
			}
		}
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid requests created runs: %d %v", count, err)
	}
}

func TestRunnerDashboardAuthAndMethods(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, tc := range []struct {
		method, path, token, csrf string
		status                    int
	}{
		{"POST", "/api/runs", "", "", http.StatusUnauthorized},
		{"POST", "/api/runs", token, "", http.StatusForbidden},
		{"POST", "/api/runs", token, "wrong", http.StatusForbidden},
		{"GET", "/api/runs/latest", "", "", http.StatusUnauthorized},
		{"GET", "/api/runs/latest", token, "", http.StatusOK},
		{"POST", "/api/runs/latest", token, "", http.StatusForbidden},
		{"POST", "/api/runs/latest", token, csrf, http.StatusMethodNotAllowed},
	} {
		w := authTestRequest(x.runsAPI, tc.method, tc.path, `{"mode":"run","lesson_count":2}`, tc.token, tc.csrf)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d %s; want %d", tc.method, tc.path, w.Code, w.Body.String(), tc.status)
		}
	}
}

func TestRunnerDashboardStartErrors(t *testing.T) {
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	if _, err := x.db.Exec(`DELETE FROM user_settings WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	body := `{"mode":"run","lesson_count":2}`
	w := authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), errCredentials.Error()) {
		t.Fatalf("missing credentials: %d %s", w.Code, w.Body.String())
	}
	saveRunnerCredentials(t, x)
	x.workerMu.Lock()
	w = authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
	x.workerMu.Unlock()
	if w.Code != http.StatusConflict {
		t.Fatalf("busy: %d %s", w.Code, w.Body.String())
	}
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_start BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'full'); END`); err != nil {
		t.Fatal(err)
	}
	w = authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), errRunStorage.Error()) {
		t.Fatalf("storage: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM user_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("one-off persisted settings: %d %v", count, err)
	}
}

func dashboardTestRun(t *testing.T, x *app, userID int64, mode, status string, events ...string) int64 {
	t.Helper()
	finished := 456
	if status == "running" {
		finished = 0
	}
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,summary,created_at,finished_at) VALUES(?,?,'','manual',?,'Saved summary',123,?)`, userID, mode, status, finished)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range events {
		if _, err := x.db.Exec(`INSERT INTO run_events(run_id,seq,data) VALUES(?,?,?)`, id, i+1, data); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestRunnerDashboardLatestOwnershipAndFilters(t *testing.T) {
	x := newAuthTestApp(t)
	owner := authTestUser(t, x, "owner@example.com", false)
	token, _ := authTestSession(t, x, owner)
	assertEmpty := func() {
		t.Helper()
		w := authTestRequest(x.runsAPI, "GET", "/api/runs/latest", "", token, "")
		var response map[string]json.RawMessage
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response) != 2 || string(response["run"]) != "null" || string(response["result"]) != "null" {
			t.Fatalf("empty latest: %d %s", w.Code, w.Body.String())
		}
	}
	assertEmpty()
	dashboardTestRun(t, x, owner, "classes", "completed", `{"type":"result","success":true}`)
	dashboardTestRun(t, x, owner, "scrape", "completed", `{"type":"result","success":true}`)
	dashboardTestRun(t, x, 1, "run", "completed", `{"type":"result","words_learned":999}`)
	assertEmpty()
	result := `{"type":"result","success":false,"lessons_completed":2,"words_learned":5,"errors_made":3,"error_code":"worker_error","message":"Saved error","duration_seconds":12.5,"challenge_snapshot":{"group_id":"42","lessons_completed_at_snapshot":1,"available":2,"in_progress":1,"completed":0,"active_id":"9007199254740993","challenges":[{"id":"9007199254740993","title":"<untrusted>","description":"Observed","points":7,"status":"in_progress"}]}}`
	legacy := strings.TrimSuffix(result, "}") + `,"username":"private@example.com","credentials":{"password":"raw-secret"},"classes":[{}],"scrape":{}}`
	id := dashboardTestRun(t, x, owner, "run", "failed", `{"type":"result","lessons_completed":1}`, legacy, `{"type":"progress"}`, "malformed legacy JSON", `null`, `[]`, `{"type":{"nested":"result"}}`)
	for i := 0; i < 60; i++ {
		dashboardTestRun(t, x, owner, "classes", "completed", `{"type":"result","success":true}`)
	}
	dashboardTestRun(t, x, owner, "scrape", "completed", `{"type":"result","success":true}`)
	dashboardTestRun(t, x, owner, "run", "running", `{"type":"result","words_learned":888}`)
	dashboardTestRun(t, x, 1, "run", "completed", `{"type":"result","words_learned":777}`)
	w := authTestRequest(x.runsAPI, "GET", "/api/runs/latest", "", token, "")
	var response struct {
		Run    *RunRecord     `json:"run"`
		Result map[string]any `json:"result"`
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(result), &want); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Run == nil || response.Run.ID != id || response.Run.UserID != owner || response.Run.Status != "failed" || response.Run.CreatedAt != 123 || response.Run.FinishedAt != 456 || response.Run.Summary != "Saved summary" || !reflect.DeepEqual(response.Result, want) {
		t.Fatalf("latest result: %d %s", w.Code, w.Body.String())
	}
}

func TestRunnerDashboardLatestWithoutResult(t *testing.T) {
	x := newAuthTestApp(t)
	token, _ := authTestSession(t, x, 1)
	dashboardTestRun(t, x, 1, "run", "completed", `{"type":"result","lessons_completed":9}`)
	for _, status := range []string{"failed", "cancelled", "interrupted", "completed"} {
		id := dashboardTestRun(t, x, 1, "run", status, "malformed", `{"type":"progress"}`, `{"type":"result"`)
		w := authTestRequest(x.runsAPI, "GET", "/api/runs/latest", "", token, "")
		var response struct {
			Run    *RunRecord      `json:"run"`
			Result json.RawMessage `json:"result"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Run == nil || response.Run.ID != id || response.Run.Status != status || string(response.Result) != "null" {
			t.Fatalf("%s without result: %d %s", status, w.Code, w.Body.String())
		}
	}
}

func TestRunnerDashboardLatestStorageErrors(t *testing.T) {
	for _, kind := range []string{"runs", "run_events", "scan"} {
		t.Run(kind, func(t *testing.T) {
			x := newAuthTestApp(t)
			token, _ := authTestSession(t, x, 1)
			if kind == "runs" {
				if _, err := x.db.Exec(`DROP TABLE runs`); err != nil {
					t.Fatal(err)
				}
			} else {
				dashboardTestRun(t, x, 1, "run", "completed")
				query := `DROP TABLE run_events`
				if kind == "scan" {
					query = `UPDATE runs SET created_at='invalid'`
				}
				if _, err := x.db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			w := authTestRequest(x.runsAPI, "GET", "/api/runs/latest", "", token, "")
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "Could not load") || strings.Contains(w.Body.String(), "SQL") {
				t.Fatalf("storage error %s: %d %s", kind, w.Code, w.Body.String())
			}
		})
	}
}
