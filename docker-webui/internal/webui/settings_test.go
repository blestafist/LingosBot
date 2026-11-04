package webui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkerExecutionLimit(t *testing.T) {
	for _, value := range []string{"0", "86401", "bad", "-1"} {
		t.Setenv("WORKER_MAX_RUN_SECONDS", value)
		if _, err := workerExecutionLimit(); err == nil {
			t.Fatalf("accepted limit %q", value)
		}
	}
	for _, value := range []string{"", "1", "86400"} {
		t.Setenv("WORKER_MAX_RUN_SECONDS", value)
		if limit, err := workerExecutionLimit(); err != nil || limit < 1 || limit > 86400 {
			t.Fatalf("limit %q: %d %v", value, limit, err)
		}
	}
}

func TestWorkerSettingsValidation(t *testing.T) {
	if err := defaultSettings().validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*WorkerSettings){
		"lesson zero":        func(s *WorkerSettings) { s.LessonCount = 0 },
		"lesson high":        func(s *WorkerSettings) { s.LessonCount = 10001 },
		"empty class":        func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{" ": 0} },
		"class negative":     func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{"A": -1} },
		"class high":         func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{"A": 10001} },
		"class aggregate":    func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{"A": 6000, "B": 4001} },
		"class case":         func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{"Polski": 1, "POLSKI": 1} },
		"unicode class case": func(s *WorkerSettings) { s.ClassLessonCounts = map[string]int{"ŁÓDŹ": 1, "łódź": 1} },
		"cap zero":           func(s *WorkerSettings) { s.LessonPromptSafetyCap = 0 },
		"cap high":           func(s *WorkerSettings) { s.LessonPromptSafetyCap = 10001 },
		"errors negative":    func(s *WorkerSettings) { s.ErrorsPer100Words = -1 },
		"errors high":        func(s *WorkerSettings) { s.ErrorsPer100Words = 101 },
		"delay negative":     func(s *WorkerSettings) { s.AnswerDelayMinMs = -1 },
		"delay reversed":     func(s *WorkerSettings) { s.AnswerDelayMinMs = 1 },
		"delay high":         func(s *WorkerSettings) { s.AnswerDelayMaxMs = 60001 },
		"timeout zero":       func(s *WorkerSettings) { s.RequestTimeoutSeconds = 0 },
		"timeout high":       func(s *WorkerSettings) { s.RequestTimeoutSeconds = 301 },
		"run zero":           func(s *WorkerSettings) { s.MaxRunSeconds = 0 },
		"run high":           func(s *WorkerSettings) { s.MaxRunSeconds = 86401 },
	} {
		t.Run(name, func(t *testing.T) {
			s := defaultSettings()
			change(&s)
			if err := s.validate(); err == nil {
				t.Fatalf("accepted %+v", s)
			}
		})
	}
	s := defaultSettings()
	s.LessonCount, s.LessonPromptSafetyCap, s.ErrorsPer100Words = 10000, 10000, 100
	s.ClassLessonCounts = map[string]int{"skip": 0, "A": 6000, "B": 4000}
	s.AnswerDelayMinMs, s.AnswerDelayMaxMs, s.RequestTimeoutSeconds, s.MaxRunSeconds = 60000, 60000, 300, 86400
	if err := s.validate(); err != nil {
		t.Fatalf("upper boundaries rejected: %v", err)
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(encoded, &fields)
	for _, key := range []string{"baseUrl", "studentDashboardUrl", "lessonCount", "classLessonCounts", "lessonPromptSafetyCap", "errorsPer100Words", "answerDelayMinMs", "answerDelayMaxMs", "requestTimeoutSeconds", "maxRunSeconds"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing camelCase field %s", key)
		}
	}
	if len(fields) != 10 {
		t.Fatalf("unexpected worker fields: %s", encoded)
	}
}

func TestWorkerEndpointValidation(t *testing.T) {
	for _, tc := range []struct{ base, dashboard string }{
		{"http://169.254.169.254", "/student/dashboard"}, {"https://user@lingos.pl", "/student/dashboard"},
		{"https://lingos.pl/path", "/student/dashboard"}, {"https://lingos.pl/a/..", "/student/dashboard"},
		{"https://lingos.pl?", "/student/dashboard"}, {"https://lingos.pl#", "/student/dashboard"},
		{"https://lingos.pl", "https://evil.test/student/dashboard"}, {"https://lingos.pl", "//evil.test"},
		{"https://lingos.pl", "https://user@lingos.pl/student/dashboard"}, {"https://lingos.pl", "/student/dashboard?"},
		{"https://lingos.pl", "/student/dashboard#"}, {"https://lingos.pl", "\\\\evil.test"},
	} {
		if err := validateWorkerEndpoints(tc.base, tc.dashboard); err == nil {
			t.Errorf("accepted %q / %q", tc.base, tc.dashboard)
		}
	}
	for _, base := range []string{"https://lingos.pl", "http://localhost:8000", "http://127.0.0.1:8000", "http://[::1]:8000"} {
		if err := validateWorkerEndpoints(base, "/student/dashboard"); err != nil {
			t.Errorf("%s: %v", base, err)
		}
	}
}

func TestSettingsAPIPersistenceAndEndpointPolicy(t *testing.T) {
	x := scheduleTestApp(t)
	if w := scheduleRequest(x.settingsAPI, "GET", "/api/settings", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	s := defaultSettings()
	s.LessonCount, s.ErrorsPer100Words = 42, 17
	s.ClassLessonCounts = map[string]int{"Class A": 0, "Class B": 10}
	s.AnswerDelayMinMs, s.AnswerDelayMaxMs, s.RequestTimeoutSeconds, s.MaxRunSeconds = 100, 500, 80, 1234
	encoded, _ := json.Marshal(s)
	w := scheduleRequest(x.settingsAPI, "PUT", "/api/settings", string(encoded), true)
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	got, err := x.loadSettings(1)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("saved %+v, want %+v: %v", got, s, err)
	}
	w = scheduleRequest(x.settingsAPI, "GET", "/api/settings", "", true)
	var response struct {
		Settings            WorkerSettings `json:"settings"`
		BaseURL             string         `json:"baseUrl"`
		StudentDashboardURL string         `json:"studentDashboardUrl"`
		EndpointsManaged    bool           `json:"endpointsManaged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || !response.EndpointsManaged || !reflect.DeepEqual(response.Settings, s) || response.BaseURL != s.BaseURL || response.StudentDashboardURL != s.StudentDashboardURL {
		t.Fatalf("response %s: %v", w.Body.String(), err)
	}
	for _, change := range []func(*WorkerSettings){
		func(s *WorkerSettings) { s.BaseURL = "http://127.0.0.1:8000" },
		func(s *WorkerSettings) { s.StudentDashboardURL = "/admin" },
	} {
		malicious := s
		change(&malicious)
		encoded, _ = json.Marshal(malicious)
		if w := scheduleRequest(x.settingsAPI, "PUT", "/api/settings", string(encoded), true); w.Code != 400 {
			t.Fatalf("endpoint override: %d: %s", w.Code, w.Body.String())
		}
	}
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,created_at) VALUES(2,'second@example.com',X'00',0)`); err != nil {
		t.Fatal(err)
	}
	second, err := x.loadSettings(2)
	if err != nil || second.LessonCount != 1 {
		t.Fatalf("settings leaked across users: %+v, %v", second, err)
	}
	// Even manually corrupted endpoint values in an old row must not reach the runner.
	if _, err := x.db.Exec(`UPDATE user_settings SET config=? WHERE user_id=1`, `{"lessonCount":42,"baseUrl":"http://169.254.169.254","studentDashboardUrl":"http://evil.test"}`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_BASE_URL", "https://example.com")
	t.Setenv("WORKER_DASHBOARD_URL", "/custom/dashboard")
	got, err = x.loadSettings(1)
	if err != nil || got.BaseURL != "https://example.com" || got.StudentDashboardURL != "/custom/dashboard" {
		t.Fatalf("deployment policy bypass: %+v, %v", got, err)
	}
}

func TestLegacyScheduleMigrationPreservesData(t *testing.T) {
	x := scheduleTestApp(t)
	t.Setenv("TZ", "Europe/Warsaw")
	zone, _ := time.LoadLocation("Europe/Warsaw")
	today := time.Now().In(zone).Format("2006-01-02")
	if _, err := x.db.Exec(`DELETE FROM user_schedules; DELETE FROM user_settings; DELETE FROM settings_meta;
		UPDATE schedule SET enabled=1,time='09:15',lessons=7,errors=23,last_date=? WHERE id=1`, today); err != nil {
		t.Fatal(err)
	}
	if err := initSettingsDB(x.db); err != nil {
		t.Fatal(err)
	}
	s, err := x.loadSettings(1)
	if err != nil || s.LessonCount != 7 || s.ErrorsPer100Words != 23 {
		t.Fatalf("settings lost: %+v, %v", s, err)
	}
	got, err := x.loadSchedule(1)
	if err != nil || !got.Enabled || got.Time != "09:15" || got.Timezone != "Europe/Warsaw" || len(got.Days) != 7 || got.LastRun == 0 || time.Unix(got.NextRun, 0).In(zone).Format("2006-01-02") <= today {
		t.Fatalf("schedule lost/reran claimed day: %+v, %v", got, err)
	}
	if time.Unix(got.LastRun, 0).In(zone).Format("2006-01-02 15:04") != today+" 09:15" {
		t.Fatalf("last_date not preserved: %+v", got)
	}
	if _, err := x.db.Exec(`UPDATE user_settings SET config='{"lessonCount":99}' WHERE user_id=1;
		UPDATE user_schedules SET time='10:00' WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := initDB(x.db); err != nil {
		t.Fatal(err)
	}
	if err := initSettingsDB(x.db); err != nil {
		t.Fatal(err)
	}
	s, _ = x.loadSettings(1)
	got, _ = x.loadSchedule(1)
	if s.LessonCount != 99 || got.Time != "10:00" {
		t.Fatalf("restart overwrote migrated values: %+v / %+v", s, got)
	}
}

func TestMigrationDoesNotOverwriteExistingSettings(t *testing.T) {
	x := scheduleTestApp(t)
	if _, err := x.db.Exec(`DELETE FROM settings_meta; UPDATE user_settings SET config='{"lessonCount":321}' WHERE user_id=1;
		UPDATE schedule SET lessons=2,errors=11 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := initSettingsDB(x.db); err != nil {
		t.Fatal(err)
	}
	s, err := x.loadSettings(1)
	if err != nil || s.LessonCount != 321 {
		t.Fatalf("existing settings overwritten: %+v, %v", s, err)
	}
	if w := scheduleRequest(x.settingsAPI, "PUT", "/api/settings", `{"lessonCount":10001}`, true); w.Code != 400 || !strings.Contains(w.Body.String(), "lessonCount") {
		t.Fatalf("invalid settings: %d, %s", w.Code, w.Body.String())
	}
}
