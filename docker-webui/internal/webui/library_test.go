package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func integrationApp(t *testing.T) *app {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := aes.NewCipher(make([]byte, 32))
	a, _ := cipher.NewGCM(b)
	ctx, cancel := context.WithCancel(context.Background())
	x := &app{db: db, aead: a, admin: "admin@example.com", password: "test-password-long", ctx: ctx}
	t.Cleanup(func() { cancel(); x.workers.Wait(); db.Close() })
	for _, init := range []func() error{func() error { return initDB(db) }, x.bootstrapAdmin, func() error { return initSettingsDB(db) }, func() error { return initRunnerDB(db) }, func() error { return initLibraryDB(db) }} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	return x
}

func loginCookie(t *testing.T, x *app) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "/login", strings.NewReader("email=admin%40example.com&password=test-password-long"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	x.login(w, r)
	if w.Code != 303 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func fixtureVocabulary() json.RawMessage {
	return json.RawMessage(`{"class":{"title":"Class <script>","group_id":"42"},"wordsets":[{"info":{"id":"9","name":"Home","expected_count":1},"entries":[{"word":"a|b <img src=x>","translation":"[click](javascript:alert(1))"}]},{"info":{"id":"10","name":"Travel","expected_count":0},"entries":[]}]}`)
}

func TestLibraryPersistentTopicsExportsAndIsolation(t *testing.T) {
	x := integrationApp(t)
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	if err := x.saveVocabulary(1, "99", fixtureVocabulary()); err == nil {
		t.Fatal("accepted wrong class")
	}
	cookie := loginCookie(t, x)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		x.routes().ServeHTTP(w, r)
		return w
	}
	w := get("/api/library")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Travel") {
		t.Fatalf("library: %d %s", w.Code, w.Body.String())
	}
	var id int64
	if err := x.db.QueryRow(`SELECT id FROM library_topics WHERE user_id=1 AND name='Home'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w = get("/api/library/export?format=md")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<img") || !strings.Contains(w.Body.String(), `a\|b &lt;img`) || strings.Contains(w.Body.String(), "[click](") {
		t.Fatalf("unsafe MD: %s", w.Body.String())
	}
	w = get("/api/library/export?format=json")
	var out struct {
		Classes []scrapedClass `json:"classes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Classes) != 1 || len(out.Classes[0].Wordsets) != 2 {
		t.Fatalf("JSON: %s %v", w.Body.String(), err)
	}
	if out.Classes[0].Wordsets[0].Entries[0].Word != "a|b <img src=x>" {
		t.Fatal("JSON must retain the original text")
	}
	if _, err := x.db.Exec(`INSERT INTO users(id,email,password_hash,admin,disabled,created_at) VALUES(2,'other@example.com',x'00',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	// Move data to another owner: guessing a class or topic must not reveal it.
	if _, err := x.db.Exec(`INSERT INTO library_classes SELECT 2,class_id,title,collected_at FROM library_classes WHERE user_id=1;
		UPDATE library_topics SET user_id=2;DELETE FROM library_classes WHERE user_id=1;`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/library/export?format=json&class=42", "/api/library/topics/" + strconv.FormatInt(id, 10)} {
		if w := get(path); w.Code != 404 {
			t.Fatalf("cross-user %s: %d", path, w.Code)
		}
	}
	if w := get("/api/library/export?format=html"); w.Code != 400 {
		t.Fatal("invalid format accepted")
	}
}

func TestLibraryReplacementIsAtomic(t *testing.T) {
	x := integrationApp(t)
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_topic BEFORE INSERT ON library_topics BEGIN SELECT RAISE(ABORT,'full'); END`); err != nil {
		t.Fatal(err)
	}
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err == nil {
		t.Fatal("expected storage failure")
	}
	var count int
	if err := x.db.QueryRow(`SELECT COUNT(*) FROM library_topics`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("lost saved words: %d %v", count, err)
	}
}

func libraryTopicIDs(t *testing.T, x *app, userID int64, classID string) []int64 {
	t.Helper()
	rows, err := x.db.Query(`SELECT id FROM library_topics WHERE user_id=? AND class_id=? ORDER BY id`, userID, classID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func libraryExportBody(t *testing.T, format string, ids []int64) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"format": format, "topics": ids})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestLibraryBatchExportSelection(t *testing.T) {
	x := integrationApp(t)
	var fixture scrapedClass
	if err := json.Unmarshal(fixtureVocabulary(), &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range []cachedClass{{Title: "Class <script>", GroupID: "42"}, {Title: "ALPHA", GroupID: "13"}, {Title: "alpha", GroupID: "12"}} {
		fixture.Class = c
		data, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.saveVocabulary(1, c.GroupID, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.saveVocabulary(1, "99", json.RawMessage(`{"class":{"title":"Empty","group_id":"99"},"wordsets":[]}`)); err != nil {
		t.Fatal(err)
	}
	z, a, b := libraryTopicIDs(t, x, 1, "42"), libraryTopicIDs(t, x, 1, "12"), libraryTopicIDs(t, x, 1, "13")
	selected := []int64{z[0], b[1], a[1], a[0], z[0]}
	token, csrf := authTestSession(t, x, 1)
	for _, format := range []string{"json", "md"} {
		t.Run(format, func(t *testing.T) {
			w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", libraryExportBody(t, format, selected), token, csrf)
			if w.Code != 200 || w.Header().Get("Content-Disposition") != fmt.Sprintf(`attachment; filename="vocabulary.%s"`, format) {
				t.Fatalf("export: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if format == "json" {
				var out struct {
					Classes []scrapedClass `json:"classes"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Classes) != 3 {
					t.Fatalf("classes: %s", w.Body.String())
				}
				for i, want := range []struct {
					classID string
					topics  string
				}{{"12", "9,10"}, {"13", "10"}, {"42", "9"}} {
					c := out.Classes[i]
					var topics []string
					for _, topic := range c.Wordsets {
						topics = append(topics, topic.Info.ID)
					}
					if c.Class.GroupID != want.classID || strings.Join(topics, ",") != want.topics {
						t.Fatalf("selection/order: %s", w.Body.String())
					}
				}
				if len(out.Classes[1].Wordsets[0].Entries) != 0 || out.Classes[2].Wordsets[0].Entries[0].Word != "a|b <img src=x>" {
					t.Fatalf("empty topic or original text lost: %s", w.Body.String())
				}
			} else {
				body := w.Body.String()
				if w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || strings.Count(body, "# Vocabulary\n") != 1 || strings.Count(body, "\n## ") != 3 || strings.Count(body, "\n### ") != 4 {
					t.Fatalf("combined Markdown: %v %s", w.Header(), body)
				}
				if strings.Contains(body, "<script>") || strings.Contains(body, "<img") || strings.Contains(body, "[click](") || !strings.Contains(body, `a\|b &lt;img`) {
					t.Fatalf("unsafe Markdown: %s", body)
				}
				if strings.Index(body, "Class ID: 12") > strings.Index(body, "Class ID: 13") || strings.Index(body, "Class ID: 13") > strings.Index(body, "Class ID: 42") {
					t.Fatalf("class order: %s", body)
				}
			}
			reordered := []int64{a[0], a[1], b[1], z[0]}
			other := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", libraryExportBody(t, format, reordered), token, csrf)
			if other.Code != 200 || other.Body.String() != w.Body.String() {
				t.Fatalf("request order/duplicates changed export: %d %s", other.Code, other.Body.String())
			}
		})
	}
	for _, test := range []struct {
		query string
		want  string
	}{{"class=42", `"name":"Travel"`}, {"topic=" + strconv.FormatInt(z[1], 10), `"wordsets":[{"info":{"id":"10"`}, {"class=99", `"wordsets":[]`}} {
		w := authTestRequest(x.routes().ServeHTTP, "GET", "/api/library/export?format=json&"+test.query, "", token, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), test.want) {
			t.Fatalf("GET filter %s: %d %s", test.query, w.Code, w.Body.String())
		}
	}
}

func TestLibraryBatchExportValidation(t *testing.T) {
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	for _, body := range []string{
		``, `{`, `null`, `[]`, `true`,
		`{}`, `{"format":"json"}`, `{"topics":[1]}`,
		`{"format":"json","topics":null}`, `{"format":"json","topics":[]}`,
		`{"format":"html","topics":[1]}`, `{"format":null,"topics":[1]}`, `{"format":1,"topics":[1]}`,
		`{"format":"json","topics":[0]}`, `{"format":"json","topics":[-1]}`, `{"format":"json","topics":[null]}`,
		`{"format":"json","topics":[1.5]}`, `{"format":"json","topics":[true]}`, `{"format":"json","topics":["1"]}`,
		`{"format":"json","topics":[9223372036854775808]}`, `{"format":"json","topics":["1) OR 1=1 --"]}`,
		`{"format":"json","topics":"1"}`, `{"format":"json","topics":[1],"class":"42"}`,
		`{"format":"json","topics":[1],"topic":1}`, `{"format":"json","topics":[1]} {}`,
		`{"format":"json","topics":[1]} trailing`,
	} {
		t.Run(body, func(t *testing.T) {
			w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", body, token, csrf)
			if w.Code != 400 || w.Header().Get("Content-Disposition") != "" {
				t.Fatalf("invalid body accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, query := range []string{"class=42", "class=", "topic=1", "format=json", "topics=1", "unknown=1"} {
		w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export?"+query, `{"format":"json","topics":[1]}`, token, csrf)
		if w.Code != 400 {
			t.Fatalf("mixed query %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	oversized := strings.Repeat(" ", 1<<20) + `{"format":"json","topics":[1]}`
	if w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", oversized, token, csrf); w.Code != 400 {
		t.Fatalf("oversized JSON: %d", w.Code)
	}
}

func TestLibraryBatchExportLimit(t *testing.T) {
	x := integrationApp(t)
	var fixture scrapedClass
	if err := json.Unmarshal(fixtureVocabulary(), &fixture); err != nil {
		t.Fatal(err)
	}
	for len(fixture.Wordsets) < 500 {
		fixture.Wordsets = append(fixture.Wordsets, fixture.Wordsets[0])
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.saveVocabulary(1, "42", data); err != nil {
		t.Fatal(err)
	}
	ids := libraryTopicIDs(t, x, 1, "42")
	if len(ids) != 500 {
		t.Fatalf("fixture topics: %d", len(ids))
	}
	token, csrf := authTestSession(t, x, 1)
	w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", libraryExportBody(t, "json", ids), token, csrf)
	var out struct {
		Classes []scrapedClass `json:"classes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || err != nil || len(out.Classes) != 1 || len(out.Classes[0].Wordsets) != 500 {
		t.Fatalf("500 topics: %d %s %v", w.Code, w.Body.String(), err)
	}
	for _, selection := range [][]int64{append(ids, ids[0]), make([]int64, 501)} {
		w = authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", libraryExportBody(t, "json", selection), token, csrf)
		if w.Code != 400 || w.Header().Get("Content-Disposition") != "" {
			t.Fatalf("501 topics: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestLibraryBatchExportAuthAndCSRF(t *testing.T) {
	x := integrationApp(t)
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	ids := libraryTopicIDs(t, x, 1, "42")
	token, csrf := authTestSession(t, x, 1)
	body := libraryExportBody(t, "json", ids[:1])
	for _, test := range []struct {
		name, token, csrf, origin string
		want                      int
	}{
		{"no session", "", csrf, "", 401},
		{"invalid session", strings.Repeat("x", 43), csrf, "", 401},
		{"no CSRF", token, "", "", 403},
		{"wrong CSRF", token, "wrong", "", 403},
		{"foreign origin", token, csrf, "https://evil.example", 403},
		{"valid", token, csrf, "", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/library/export", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", test.csrf)
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			if test.token != "" {
				r.AddCookie(&http.Cookie{Name: "session", Value: test.token})
			}
			w := httptest.NewRecorder()
			x.routes().ServeHTTP(w, r)
			if w.Code != test.want || test.want != 200 && w.Header().Get("Content-Disposition") != "" {
				t.Fatalf("auth: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLibraryBatchExportIsolationAndStaleIDs(t *testing.T) {
	x := integrationApp(t)
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	otherID := authTestUser(t, x, "other@example.com", false)
	if err := x.saveVocabulary(otherID, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	own, foreign := libraryTopicIDs(t, x, 1, "42"), libraryTopicIDs(t, x, otherID, "42")
	token, csrf := authTestSession(t, x, 1)
	assertMissing := func(ids []int64) {
		t.Helper()
		for _, format := range []string{"json", "md"} {
			w := authTestRequest(x.routes().ServeHTTP, "POST", "/api/library/export", libraryExportBody(t, format, ids), token, csrf)
			var out map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 404 || err != nil || len(out) != 1 || out["error"] == "" || w.Header().Get("Content-Disposition") != "" {
				t.Fatalf("partial/leaked export %v: %d %s", ids, w.Code, w.Body.String())
			}
		}
	}
	for _, ids := range [][]int64{{foreign[0]}, {own[0], foreign[0]}, {9223372036854775807}, {own[0], 9223372036854775807}} {
		assertMissing(ids)
	}
	if err := x.saveVocabulary(1, "42", fixtureVocabulary()); err != nil {
		t.Fatal(err)
	}
	current := libraryTopicIDs(t, x, 1, "42")
	assertMissing(own)
	assertMissing([]int64{current[0], own[0]})
}
