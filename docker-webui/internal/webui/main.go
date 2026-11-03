package webui

import (
	"bufio"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type app struct {
	db              *sql.DB
	aead            cipher.AEAD
	worker          string
	admin, password string
	workerMu        sync.Mutex
}
type creds struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	LessonCount       int    `json:"lessonCount,omitempty"`
	ErrorsPer100Words int    `json:"errorsPer100Words,omitempty"`
}

var classID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func workerArgs(mode, id string) ([]string, error) {
	args := []string{"--realtime", "--config-json-stdin"}
	switch mode {
	case "classes":
		return append(args, "--list-classes"), nil
	case "scrape":
		if !classID.MatchString(id) {
			return nil, fmt.Errorf("class must be a positive numeric ID")
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return nil, fmt.Errorf("class ID is out of range")
		}
		return append(args, "--scrape-class", id), nil
	case "run":
		return args, nil
	default:
		return nil, fmt.Errorf("invalid mode")
	}
}

func Run() {
	key := os.Getenv("ENCRYPTION_KEY")
	if key == "" {
		log.Fatal("ENCRYPTION_KEY (base64-encoded 32 bytes) required")
	}
	raw, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(raw) != 32 {
		log.Fatal("ENCRYPTION_KEY must be base64 for exactly 32 bytes")
	}
	b, e := aes.NewCipher(raw)
	if e != nil {
		log.Fatal(e)
	}
	a, e := cipher.NewGCM(b)
	if e != nil {
		log.Fatal(e)
	}
	admin, pw := os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")
	if admin == "" || pw == "" {
		log.Fatal("ADMIN_USER and ADMIN_PASSWORD required")
	}
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "data/webui.db"
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		log.Fatal(e)
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	if e = initDB(db); e != nil {
		log.Fatal(e)
	}
	x := &app{db: db, aead: a, worker: env("WORKER_BIN", "/usr/local/bin/lingosbot"), admin: admin, password: pw}
	mux := http.NewServeMux()
	mux.Handle("/static/", staticFiles())
	mux.HandleFunc("/", x.home)
	mux.HandleFunc("/login", x.login)
	mux.HandleFunc("/logout", x.logout)
	mux.HandleFunc("/credentials", x.credentials)
	mux.HandleFunc("/events", x.events)
	mux.HandleFunc("/schedule", x.saveSchedule)
	go x.scheduleLoop()
	log.Fatal(http.ListenAndServe(env("LISTEN", ":8080"), mux))
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func (x *app) session(r *http.Request) (string, string) {
	c, e := r.Cookie("session")
	if e != nil {
		return "", ""
	}
	var csrf string
	var exp int64
	if x.db.QueryRow(`SELECT csrf,expires FROM sessions WHERE token=?`, c.Value).Scan(&csrf, &exp) != nil || exp < time.Now().Unix() {
		return "", ""
	}
	return c.Value, csrf
}
func (x *app) home(w http.ResponseWriter, r *http.Request) {
	_, c := x.session(r)
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	data, ok := x.homeData(w, r, c)
	if !ok {
		return
	}
	if c != "" {
		s, err := x.loadSchedule()
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		data["Schedule"] = s
		data["ServerTimezone"] = fmt.Sprintf("%s (%s)", time.Local.String(), time.Now().Format("MST UTC-07:00"))
	}
	if err := page.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render page: %v", err)
	}
}
func (x *app) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	if r.ParseForm() != nil || r.Form.Get("user") != x.admin || r.Form.Get("password") != x.password {
		http.Redirect(w, r, "/?error=1", http.StatusSeeOther)
		return
	}
	tok := make([]byte, 32)
	csrf := make([]byte, 32)
	rand.Read(tok)
	rand.Read(csrf)
	t, c := base64.RawURLEncoding.EncodeToString(tok), base64.RawURLEncoding.EncodeToString(csrf)
	x.db.Exec(`INSERT INTO sessions VALUES(?,?,?)`, t, c, time.Now().Add(12*time.Hour).Unix())
	http.SetCookie(w, &http.Cookie{Name: "session", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: os.Getenv("COOKIE_SECURE") == "1", MaxAge: 43200})
	http.Redirect(w, r, "/", 303)
}
func (x *app) authorize(w http.ResponseWriter, r *http.Request) bool {
	tok, csrf := x.session(r)
	if tok == "" {
		http.Error(w, "unauthorized", 401)
		return false
	}
	if r.FormValue("csrf") != csrf && r.Header.Get("X-CSRF-Token") != csrf {
		http.Error(w, "forbidden", 403)
		return false
	}
	return true
}
func (x *app) logout(w http.ResponseWriter, r *http.Request) {
	if !x.authorize(w, r) {
		return
	}
	c, _ := r.Cookie("session")
	x.db.Exec(`DELETE FROM sessions WHERE token=?`, c.Value)
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", 303)
}
func (x *app) credentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || !x.authorize(w, r) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
		}
		return
	}
	p := r.FormValue("password")
	if r.FormValue("username") == "" || p == "" {
		http.Error(w, "credentials required", 400)
		return
	}
	nonce := make([]byte, x.aead.NonceSize())
	rand.Read(nonce)
	enc := x.aead.Seal(nonce, nonce, []byte(p), nil)
	_, e := x.db.Exec(`INSERT INTO credentials VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,password=excluded.password`, r.FormValue("username"), enc)
	if e != nil {
		http.Error(w, "storage error", 500)
		return
	}
	http.Redirect(w, r, "/?saved=1", 303)
}
func (x *app) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || !x.authorize(w, r) {
		if r.Method != "GET" {
			http.Error(w, "method", 405)
		}
		return
	}
	args, err := workerArgs(r.URL.Query().Get("mode"), r.URL.Query().Get("class"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	lessons, errorsPer100 := 1, 0
	if value := r.URL.Query().Get("lessons"); value != "" {
		lessons, err = strconv.Atoi(value)
		if err != nil {
			http.Error(w, "invalid lesson settings", 400)
			return
		}
	}
	if value := r.URL.Query().Get("errors"); value != "" {
		errorsPer100, err = strconv.Atoi(value)
	}
	if err != nil || lessons < 1 || lessons > 10000 || errorsPer100 < 0 || errorsPer100 > 100 {
		http.Error(w, "invalid lesson settings", 400)
		return
	}
	cfg, err := x.workerConfig(lessons, errorsPer100)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "save Lingos credentials first", 400)
		} else {
			http.Error(w, "credential storage error", 500)
		}
		return
	}
	if !x.workerMu.TryLock() {
		http.Error(w, "worker already running", http.StatusConflict)
		return
	}
	defer x.workerMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, x.worker, args...)
	cmd.Stdin = bytes.NewReader(cfg)
	out, e := cmd.StdoutPipe()
	if e != nil {
		http.Error(w, "worker error", 500)
		return
	}
	cmd.Stderr = io.Discard
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	if e = cmd.Start(); e != nil {
		http.Error(w, "worker unavailable", 500)
		return
	}
	defer cmd.Wait()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		fl.Flush()
	}
	if scanner.Err() != nil {
		cancel()
		fmt.Fprint(w, "event: error\ndata: {\"message\":\"Could not read the worker stream.\"}\n\n")
		fl.Flush()
	}
}
