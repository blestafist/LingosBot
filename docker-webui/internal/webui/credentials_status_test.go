package webui

import (
	"bytes"
	"database/sql"
	"html/template"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHomeDataCredentialsStatus(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE credentials (id INTEGER PRIMARY KEY, username TEXT NOT NULL, password BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	x := &app{db: db}
	r := httptest.NewRequest(http.MethodGet, "/?error=1", nil)
	tmpl := template.Must(template.New("status").Parse(`{{if .CredentialsSaved}}saved{{else}}not saved{{end}}`))

	check := func(csrf string, wantSaved bool) {
		t.Helper()
		w := httptest.NewRecorder()
		data, ok := x.homeData(w, r, csrf)
		want := map[string]any{"In": csrf != "", "CSRF": csrf, "LoginError": true, "CredentialsSaved": wantSaved}
		if !ok || !reflect.DeepEqual(data, want) {
			t.Fatalf("homeData = %v, %v; want %v, true", data, ok, want)
		}
		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, data); err != nil {
			t.Fatal(err)
		}
		wantText := "not saved"
		if wantSaved {
			wantText = "saved"
		}
		if rendered.String() != wantText {
			t.Fatalf("rendered %q; want %q", rendered.String(), wantText)
		}
	}

	check("csrf", false)
	if _, err := db.Exec(`INSERT INTO credentials VALUES (1, 'private-user', 'private-password')`); err != nil {
		t.Fatal(err)
	}
	check("csrf", true)
	check("", false)
}

func TestHomeDataDatabaseError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	x := &app{db: db}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	data, ok := x.homeData(w, r, "csrf")
	if ok || data != nil || w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "storage error" {
		t.Fatalf("homeData = %v, %v; HTTP %d %q", data, ok, w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	data, ok = x.homeData(w, r, "")
	if !ok || data["CredentialsSaved"] != false || w.Code != http.StatusOK {
		t.Fatalf("unauthenticated homeData = %v, %v; HTTP %d", data, ok, w.Code)
	}
}
