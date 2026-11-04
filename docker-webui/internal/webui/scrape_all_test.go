package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func allVocabularyFixture(t *testing.T) (json.RawMessage, json.RawMessage) {
	t.Helper()
	classes := json.RawMessage(`[{"title":"Class <script>","group_id":"42"},{"title":"Empty","group_id":"7"}]`)
	scrapes := json.RawMessage(`[` + string(fixtureVocabulary()) + `,{"class":{"title":"Empty","group_id":"7"},"wordsets":[]}]`)
	return classes, scrapes
}

func allVocabularyResult(t *testing.T, success bool, classes, scrapes json.RawMessage) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "result", "success": success, "classes": classes, "scrapes": scrapes})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func vocabularySnapshot(t *testing.T, x *app) string {
	t.Helper()
	var snapshot string
	err := x.db.QueryRow(`SELECT json_array(
		(SELECT json_group_array(json_array(user_id,class_id,title,collected_at)) FROM (SELECT * FROM library_classes ORDER BY user_id,class_id)),
		(SELECT json_group_array(json_array(id,user_id,class_id,name,count,data)) FROM (SELECT * FROM library_topics ORDER BY id)),
		(SELECT json_group_array(json_array(user_id,data)) FROM (SELECT * FROM class_cache ORDER BY user_id)))`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestScrapeAllModeValidation(t *testing.T) {
	want := []string{"--realtime", "--config-json-stdin", "--scrape-all"}
	if got, err := workerArgs("scrape_all", ""); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("scrape_all args: %v %v", got, err)
	}
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, body := range []string{
		`{"mode":"scrape_all","class_id":"42"}`,
		`{"mode":"scrape_all","class_id":" "}`,
		`{"mode":"scrape_all","lesson_count":1}`,
		`{"mode":"scrape_all","lesson_count":0}`,
		`{"mode":"scrape_all","lesson_count":null}`,
		`{"mode":"scrape_all","lesson_count":"1"}`,
		`{"mode":"scrape-all"}`,
	} {
		w := authTestRequest(x.runsAPI, "POST", "/api/runs", body, token, csrf)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid request %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid requests created runs: %d %v", count, err)
	}
	if _, err := x.startRun(1, "scrape_all", "", "manual"); !errors.Is(err, errCredentials) {
		t.Fatalf("missing credentials: %v", err)
	}
}

func TestScrapeAllRunnerSurvivesReloadAndPersistsOwnedLibrary(t *testing.T) {
	x := integrationApp(t)
	saveRunnerCredentials(t, x)
	other := authTestUser(t, x, "other@example.com", false)
	if err := x.saveVocabulary(other, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	foreignIDs := libraryTopicIDs(t, x, other, "42")
	if err := x.saveClasses(other, json.RawMessage(`[{"title":"Foreign","group_id":"99"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := x.saveVocabulary(1, "99", json.RawMessage(`{"class":{"title":"Keep absent class","group_id":"99"},"wordsets":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := x.saveVocabulary(1, "42", json.RawMessage(strings.Replace(string(fixtureVocabulary()), "a|b <img src=x>", "obsolete", 1))); err != nil {
		t.Fatal(err)
	}
	oldIDs := libraryTopicIDs(t, x, 1, "42")
	if err := x.saveVocabulary(1, "7", json.RawMessage(`{"class":{"title":"Old class","group_id":"7"},"wordsets":[{"info":{"id":"1","name":"Obsolete topic"},"entries":[]}]}`)); err != nil {
		t.Fatal(err)
	}
	classes, scrapes := allVocabularyFixture(t)
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	x.worker = filepath.Join(dir, "worker")
	script := "#!/bin/sh\ncat >/dev/null\ntest \"$#\" -eq 3 && test \"$1\" = --realtime && test \"$2\" = --config-json-stdin && test \"$3\" = --scrape-all || exit 1\n" +
		"printf '%s\\n' '{\"type\":\"http_request\",\"operation\":\"load_vocabulary\"}'\n" +
		"while [ ! -f '" + release + "' ]; do sleep 0.01; done\ncat <<'RESULT'\n" + allVocabularyResult(t, true, classes, scrapes) + "\nRESULT\n"
	if err := os.WriteFile(x.worker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	token, csrf := authTestSession(t, x, 1)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/api/runs", strings.NewReader(`{"mode":"scrape_all"}`)).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	x.runsAPI(w, r)
	cancel() // Closing/reloading the initiating page must not cancel the server task.
	var started struct {
		Run RunRecord `json:"run"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &started) != nil || started.Run.Mode != "scrape_all" || started.Run.ClassID != "" {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	path := "/api/runs/" + strconv.FormatInt(started.Run.ID, 10)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := x.db.QueryRow(`SELECT count(*) FROM run_events WHERE run_id=?`, started.Run.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached worker did not report progress")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reloadedToken, _ := authTestSession(t, x, 1)
	w = authTestRequest(x.runsAPI, "GET", path, "", reloadedToken, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"running"`) || !strings.Contains(w.Body.String(), "load_vocabulary") {
		t.Fatalf("reload: %d %s", w.Code, w.Body.String())
	}
	if _, err := x.startRun(1, "scrape_all", "", "manual"); !errors.Is(err, errWorkerBusy) {
		t.Fatalf("all-class task did not own the worker: %v", err)
	}
	foreignToken, _ := authTestSession(t, x, other)
	if w := authTestRequest(x.runsAPI, "GET", path, "", foreignToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("foreign run exposed: %d %s", w.Code, w.Body.String())
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	x.workers.Wait()
	var dbPath, dbName string
	var dbSeq int
	if err := x.db.QueryRow(`PRAGMA database_list`).Scan(&dbSeq, &dbName, &dbPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := &app{db: reopened, aead: x.aead}
	w = authTestRequest(restored.runsAPI, "GET", path, "", reloadedToken, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"completed"`) || strings.Contains(w.Body.String(), `"scrapes"`) || strings.Contains(w.Body.String(), `"classes"`) {
		t.Fatalf("persisted run: %d %s", w.Code, w.Body.String())
	}
	w = authTestRequest(restored.libraryAPI, "GET", "/api/library", "", reloadedToken, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"class_id":"42"`) || !strings.Contains(w.Body.String(), `"title":"Empty"`) || !strings.Contains(w.Body.String(), `"topics":[]`) || !strings.Contains(w.Body.String(), "Keep absent class") {
		t.Fatalf("persisted library: %d %s", w.Code, w.Body.String())
	}
	if reflect.DeepEqual(oldIDs, libraryTopicIDs(t, x, 1, "42")) || len(libraryTopicIDs(t, x, 1, "7")) != 0 || strings.Contains(w.Body.String(), "Obsolete topic") {
		t.Fatal("fresh collection did not replace existing class topics, including an emptied class")
	}
	w = authTestRequest(restored.classesAPI, "GET", "/api/classes", "", reloadedToken, "")
	var cached struct {
		Classes []cachedClass `json:"classes"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &cached) != nil || len(cached.Classes) != 2 || cached.Classes[0].GroupID != "42" || cached.Classes[1].GroupID != "7" {
		t.Fatalf("persisted class cache: %d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(foreignIDs, libraryTopicIDs(t, x, other, "42")) {
		t.Fatal("all-class collection changed another owner's topics")
	}
	w = authTestRequest(restored.classesAPI, "GET", "/api/classes", "", foreignToken, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Foreign") || strings.Contains(w.Body.String(), "Empty") {
		t.Fatalf("foreign cache changed: %d %s", w.Code, w.Body.String())
	}
}

func TestScrapeAllAtomicRollbackIncludesClassCache(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER reject_class BEFORE INSERT ON library_classes WHEN NEW.class_id='7' BEGIN SELECT RAISE(ABORT,'full'); END`,
		`CREATE TRIGGER reject_cache BEFORE UPDATE ON class_cache BEGIN SELECT RAISE(ABORT,'full'); END`,
	} {
		t.Run(trigger, func(t *testing.T) {
			x := integrationApp(t)
			if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
				t.Fatal(err)
			}
			if err := x.saveClasses(1, json.RawMessage(`[{"title":"Old","group_id":"42"}]`)); err != nil {
				t.Fatal(err)
			}
			before := vocabularySnapshot(t, x)
			if _, err := x.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			classes, scrapes := allVocabularyFixture(t)
			if err := x.saveAllVocabulary(1, classes, scrapes); err == nil {
				t.Fatal("expected transaction failure")
			}
			if got := vocabularySnapshot(t, x); got != before {
				t.Fatalf("partial commit: before %s, after %s", before, got)
			}
		})
	}
}

func TestScrapeAllRejectsMalformedAndMismatchedResultsWithoutChanges(t *testing.T) {
	x := integrationApp(t)
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	classes, scrapes := allVocabularyFixture(t)
	before := vocabularySnapshot(t, x)
	for _, tc := range []struct{ classes, scrapes string }{
		{`null`, string(scrapes)}, {`{}`, string(scrapes)}, {`[]`, `null`}, {`[]`, `{}`},
		{`[{}]`, `[]`}, {`[null]`, `[]`},
		{`[{"group_id":"42","title":"A"},{"group_id":"42","title":"A"}]`, string(scrapes)},
		{`[{"group_id":"0","title":"A"}]`, string(scrapes)},
		{`[{"group_id":"9223372036854775808","title":"A"}]`, string(scrapes)},
		{`[{"group_id":42,"title":"A"}]`, string(scrapes)},
		{`[{"group_id":"42","title":" "}]`, string(scrapes)},
		{string(classes), `[]`}, {`[]`, string(scrapes)},
		{string(classes), `[` + string(fixtureVocabulary()) + `,` + string(fixtureVocabulary()) + `]`},
		{string(classes), strings.Replace(string(scrapes), `"group_id":"7"`, `"group_id":"8"`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"title":"Empty"`, `"title":"Wrong title"`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"wordsets":[]`, `"wordsets":null`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"entries":[]`, `"entries":null`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"expected_count":1`, `"expected_count":2`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"name":"Home"`, `"name":" "`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"id":"10"`, `"id":"9"`, 1)},
		{string(classes), strings.Replace(string(scrapes), `"translation":"[click](javascript:alert(1))"`, `"translation":""`, 1)},
	} {
		if err := x.saveAllVocabulary(1, json.RawMessage(tc.classes), json.RawMessage(tc.scrapes)); err == nil {
			t.Errorf("accepted malformed batch: %s / %s", tc.classes, tc.scrapes)
		}
		if got := vocabularySnapshot(t, x); got != before {
			t.Fatalf("validation changed library/cache: %s", got)
		}
	}
	if err := x.saveAllVocabulary(1, json.RawMessage(`[]`), json.RawMessage(`[]`)); err != nil {
		t.Fatalf("zero classes: %v", err)
	}
	if ids := libraryTopicIDs(t, x, 1, "42"); len(ids) != 2 {
		t.Fatal("empty discovery removed previously collected words")
	}
	var cache string
	if err := x.db.QueryRow(`SELECT data FROM class_cache WHERE user_id=1`).Scan(&cache); err != nil || cache != "[]" {
		t.Fatalf("empty discovery did not refresh cache: %s %v", cache, err)
	}
}

func TestScrapeAllRunnerFailurePreservesLibraryAndStripsPayload(t *testing.T) {
	for _, failure := range []string{"worker-error", "invalid-batch", "exit-error", "duplicate-result", "invalid-output", "storage-error"} {
		t.Run(failure, func(t *testing.T) {
			x := integrationApp(t)
			saveRunnerCredentials(t, x)
			if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
				t.Fatal(err)
			}
			if err := x.saveClasses(1, json.RawMessage(`[{"title":"Old","group_id":"42"}]`)); err != nil {
				t.Fatal(err)
			}
			before := vocabularySnapshot(t, x)
			classes, scrapes := allVocabularyFixture(t)
			if failure == "invalid-batch" {
				scrapes = json.RawMessage(`[]`)
			}
			result := allVocabularyResult(t, failure != "worker-error", classes, scrapes)
			extra := ""
			switch failure {
			case "exit-error":
				extra = "exit 1\n"
			case "duplicate-result":
				extra = "cat <<'RESULT'\n" + result + "\nRESULT\n"
			case "invalid-output":
				extra = "printf 'invalid\\n'\n"
			case "storage-error":
				if _, err := x.db.Exec(`CREATE TRIGGER reject_cache BEFORE UPDATE ON class_cache BEGIN SELECT RAISE(ABORT,'full'); END`); err != nil {
					t.Fatal(err)
				}
			}
			x.worker = filepath.Join(t.TempDir(), "worker")
			if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\ncat <<'RESULT'\n"+result+"\nRESULT\n"+extra), 0700); err != nil {
				t.Fatal(err)
			}
			id, err := x.startRun(1, "scrape_all", "", "manual")
			if err != nil {
				t.Fatal(err)
			}
			x.workers.Wait()
			run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=?`, id))
			if err != nil || run.Status != "failed" {
				t.Fatalf("failed run: %+v %v", run, err)
			}
			if got := vocabularySnapshot(t, x); got != before {
				t.Fatalf("failure changed saved library/cache: %s", got)
			}
			var data string
			if err := x.db.QueryRow(`SELECT data FROM run_events WHERE run_id=? ORDER BY seq LIMIT 1`, id).Scan(&data); err != nil || strings.Contains(data, "scrapes") || strings.Contains(data, "classes") {
				t.Fatalf("unstripped result: %s %v", data, err)
			}
		})
	}
}

func TestScrapeAllRejectsOversizedBatches(t *testing.T) {
	x := integrationApp(t)
	classes, scrapes := allVocabularyFixture(t)
	var parsed []scrapedClass
	if err := json.Unmarshal(scrapes, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []string{"classes", "wordsets", "cards", "term", "total-text"} {
		t.Run(limit, func(t *testing.T) {
			var batch []scrapedClass
			if err := json.Unmarshal(scrapes, &batch); err != nil {
				t.Fatal(err)
			}
			classList := classes
			switch limit {
			case "classes":
				var list []cachedClass
				for i := 1; i <= 257; i++ {
					list = append(list, cachedClass{Title: "A", GroupID: strconv.Itoa(i)})
				}
				classList, _ = json.Marshal(list)
			case "wordsets":
				for i := 1; i <= 256; i++ {
					topic := parsed[0].Wordsets[1]
					topic.Info.ID = strconv.Itoa(i)
					batch[1].Wordsets = append(batch[1].Wordsets, topic)
				}
			case "cards":
				topic := &batch[0].Wordsets[0]
				topic.Info.ExpectedCount = nil
				for len(topic.Entries) <= 10000 {
					topic.Entries = append(topic.Entries, topic.Entries[0])
				}
			case "term":
				batch[0].Wordsets[0].Entries[0].Word = strings.Repeat("x", (16<<10)+1)
			case "total-text":
				topic := &batch[0].Wordsets[0]
				topic.Info.ExpectedCount = nil
				topic.Entries[0].Word = strings.Repeat("x", 16<<10)
				for len(topic.Entries) < 64 {
					topic.Entries = append(topic.Entries, topic.Entries[0])
				}
			}
			raw, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			if err := x.saveAllVocabulary(1, classList, raw); err == nil {
				t.Fatalf("accepted oversized %s batch", limit)
			}
		})
	}
}
