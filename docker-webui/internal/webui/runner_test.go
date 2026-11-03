package webui

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWorkerArgs(t *testing.T) {
	got, err := workerArgs("scrape", "12345")
	want := []string{"--realtime", "--config-json-stdin", "--scrape-class", "12345"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("workerArgs = %v, %v; want %v", got, err, want)
	}
	for _, id := range []string{"0", "12x", "9223372036854775808"} {
		if _, err := workerArgs("scrape", id); err == nil {
			t.Errorf("workerArgs accepted invalid class ID %q", id)
		}
	}
}

func TestRunnerRecoversFailedFinalWriteAndReportsStorageErrors(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"success\":true}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_finish BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT,'full'); END`); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(1, "run", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	if _, err := x.db.Exec(`DROP TRIGGER reject_finish`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.startRun(1, "run", "", "manual"); err != nil {
		t.Fatalf("stale run blocked launch: %v", err)
	}
	x.workers.Wait()
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
	if err != nil || run.Status != "interrupted" {
		t.Fatalf("stale recovery: %+v %v", run, err)
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_start BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'full'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := x.startRun(1, "run", "", "manual"); !errors.Is(err, errRunStorage) || errors.Is(err, errWorkerBusy) {
		t.Fatalf("storage error masked as busy: %v", err)
	}
}

func TestRunnerPreservesSavedResultOnLateCancellation(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"result\",\"success\":true,\"lessons_completed\":3}'\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(1, "run", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := x.db.QueryRow(`SELECT count(*) FROM run_events WHERE run_id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("result not saved")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := x.cancelUserRuns(1); err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
	if err != nil || run.Status != "completed" || !strings.Contains(run.Summary, "3 lessons") {
		t.Fatalf("late cancellation lost result: %+v %v", run, err)
	}
}

func TestRunnerServerDeadlineAndInvalidOutput(t *testing.T) {
	for _, tc := range []struct{ name, output, summary string }{
		{"deadline", "exec sleep 60", "maximum run duration"},
		{"invalid-output", "printf 'invalid\\n'", "invalid output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WORKER_MAX_RUN_SECONDS", "1")
			x := integrationApp(t)
			saveRunnerCredentials(t, x)
			x.worker = filepath.Join(t.TempDir(), "worker")
			capture := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat > '"+capture+"'\n"+tc.output+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			id, err := x.startRun(1, "run", "", "manual")
			if err != nil {
				t.Fatal(err)
			}
			x.workers.Wait()
			run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
			if err != nil || run.Status != "failed" || !strings.Contains(run.Summary, tc.summary) {
				t.Fatalf("run: %+v %v", run, err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var settings WorkerSettings
			if err := json.Unmarshal(raw, &settings); err != nil || settings.MaxRunSeconds != 1 {
				t.Fatalf("uncapped config: %s %v", raw, err)
			}
		})
	}
}

func TestRunnerDoesNotInheritSecretsWithoutWhitelistedEnvironment(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ADMIN_PASSWORD", "server-secret")
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ntest -z \"$ADMIN_PASSWORD\" || exit 1\nprintf '%s\\n' '{\"type\":\"result\",\"success\":true}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(1, "run", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
	if err != nil || run.Status != "completed" {
		t.Fatalf("empty allowlist leaked secrets: %+v %v", run, err)
	}
}

func saveRunnerCredentials(t *testing.T, x *app) {
	t.Helper()
	cookie := loginCookie(t, x)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookie)
	_, csrf := x.session(r)
	r = httptest.NewRequest("PUT", "/api/credentials", strings.NewReader(`{"email":"learner@example.com","password":"worker-secret"}`))
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	x.credentialsAPI(w, r)
	if w.Code != 200 {
		t.Fatalf("save creds: %d %s", w.Code, w.Body.String())
	}
}

func TestRunnerPersistsProgressAndExactConfiguration(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	dir := t.TempDir()
	capture := filepath.Join(dir, "config")
	x.worker = filepath.Join(dir, "worker")
	t.Setenv("LINGOS_USERNAME", "must-not-override")
	t.Setenv("ADMIN_PASSWORD", "server-secret")
	script := "#!/bin/sh\ncat > '" + capture + "'\ntest -z \"$LINGOS_USERNAME$ADMIN_PASSWORD$ENCRYPTION_KEY\" || exit 1\nprintf '%s\\n' '{\"type\":\"lesson_start\",\"lesson\":1,\"lessons_total\":2,\"class_title\":\"A\"}' '{\"type\":\"result\",\"success\":true,\"lessons_completed\":2,\"words_learned\":8}'\n"
	if err := os.WriteFile(x.worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(1, "run", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
	if err != nil || run.Status != "completed" || !strings.Contains(run.Summary, "8 words") {
		t.Fatalf("run: %+v %v", run, err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"lessonCount", "classLessonCounts", "lessonPromptSafetyCap", "errorsPer100Words", "answerDelayMinMs", "answerDelayMaxMs", "requestTimeoutSeconds", "maxRunSeconds", "baseUrl", "studentDashboardUrl", "credentials"} {
		if _, ok := config[key]; !ok {
			t.Errorf("missing Rust config field %s", key)
		}
	}
	if !strings.Contains(string(config["credentials"]), "worker-secret") {
		t.Fatal("credentials not passed via stdin")
	}
	cookie := loginCookie(t, x)
	r := httptest.NewRequest("GET", "/api/runs/"+strconv.FormatInt(id, 10)+"?after=0", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	x.runsAPI(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "lesson_start") || strings.Contains(w.Body.String(), "worker-secret") {
		t.Fatalf("progress: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.db.Exec(`UPDATE runs SET user_id=2 WHERE id=?`, id); err == nil {
		t.Fatal("FK must reject unknown owner")
	}
}

func TestRunnerBusyCancellationAndScheduleDeduplication(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startScheduledRun(1, 12345)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.startRun(1, "run", "", "manual"); err != errWorkerBusy {
		t.Fatalf("busy: %v", err)
	}
	if same, err := x.startScheduledRun(1, 12345); err != nil || same != id {
		t.Fatalf("deduplication: %d %v", same, err)
	}
	if err := x.cancelUserRuns(1); err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
	if err != nil || run.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", run, err)
	}
	if same, err := x.startScheduledRun(1, 12345); err != nil || same != id {
		t.Fatal("replayed a cancelled scheduled slot")
	}
}

func TestRunnerStoresVocabularyInsteadOfDuplicatingLargeEvents(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	x.worker = filepath.Join(t.TempDir(), "worker")
	event := `{"type":"result","success":true,"scrape":` + string(fixtureVocabulary()) + `}`
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'RESULT'\n"+event+"\nRESULT\n"), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(1, "scrape", "42", "manual")
	if err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	var count int
	if err := x.db.QueryRow(`SELECT COUNT(*) FROM library_topics WHERE user_id=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("topics: %d %v", count, err)
	}
	var data string
	if err := x.db.QueryRow(`SELECT data FROM run_events WHERE run_id=?`, id).Scan(&data); err != nil || strings.Contains(data, "scrape") {
		t.Fatalf("event: %s %v", data, err)
	}
}
