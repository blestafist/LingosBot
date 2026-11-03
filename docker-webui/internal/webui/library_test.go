package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
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
