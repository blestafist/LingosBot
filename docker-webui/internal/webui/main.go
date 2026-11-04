package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type app struct {
	db              *sql.DB
	aead            cipher.AEAD
	worker          string
	admin, password string
	workerMu        sync.Mutex
	runMu           sync.Mutex
	cancels         map[int64]context.CancelFunc
	workers         sync.WaitGroup
	ctx             context.Context
	telegram        *telegramBot
}

func Run() {
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

func serve() error {
	if _, err := workerExecutionLimit(); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(raw) != 32 {
		return errors.New("ENCRYPTION_KEY must be base64 for exactly 32 bytes; keep the same key across restarts")
	}
	b, err := aes.NewCipher(raw)
	if err != nil {
		return err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return err
	}
	path := env("DB_PATH", "data/webui.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// One server owns this database: recovery must never interrupt another server's runs.
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another WebUI process is already using this database")
	}
	// Set permissions before SQLite creates WAL/SHM files, which inherit the database mode.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = file.Chmod(0600)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dsn := url.URL{Scheme: "file", Path: absolute, RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initDB(db); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	x := &app{db: db, aead: a, worker: env("WORKER_BIN", "/usr/local/bin/lingosbot"), admin: os.Getenv("ADMIN_USER"), password: os.Getenv("ADMIN_PASSWORD"), ctx: ctx}
	if err := x.bootstrapAdmin(); err != nil {
		return err
	}
	if err := initSettingsDB(db); err != nil {
		return err
	}
	if err := initRunnerDB(db); err != nil {
		return err
	}
	if err := initLibraryDB(db); err != nil {
		return err
	}
	if err := initTelegramDB(db); err != nil {
		return err
	}
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		x.telegram = newTelegramBot(token)
	}
	if _, err := db.Exec(`UPDATE runs SET status='interrupted',summary='The server stopped during this run. Check Lingos before retrying.',finished_at=? WHERE status='running'`, time.Now().Unix()); err != nil {
		return err
	}
	server := &http.Server{Addr: env("LISTEN", "127.0.0.1:8080"), Handler: x.secure(x.routes()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	scheduleDone := make(chan struct{})
	go func() {
		defer close(scheduleDone)
		x.scheduleLoop(ctx)
	}()
	telegramDone := make(chan struct{})
	go func() {
		defer close(telegramDone)
		if x.telegram != nil {
			x.telegramLoop(ctx)
		}
	}()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("WebUI listening on %s", server.Addr)
	err = server.ListenAndServe()
	stop()
	<-shutdownDone
	<-scheduleDone
	<-telegramDone
	x.workers.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (x *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", staticFiles())
	mux.HandleFunc("GET /{$}", x.home)
	mux.HandleFunc("POST /login", x.login)
	mux.HandleFunc("POST /logout", x.logout)
	mux.HandleFunc("/api/account", x.accountAPI)
	mux.HandleFunc("/api/credentials", x.credentialsAPI)
	mux.HandleFunc("/api/telegram", x.telegramAPI)
	mux.HandleFunc("/api/telegram/link", x.telegramLinkAPI)
	mux.HandleFunc("/api/admin/users", x.adminUsersAPI)
	mux.HandleFunc("/api/admin/users/", x.adminUsersAPI)
	mux.HandleFunc("/api/admin/telegram", x.adminTelegramAPI)
	mux.HandleFunc("/api/admin/telegram/", x.adminTelegramAPI)
	mux.HandleFunc("/api/settings", x.settingsAPI)
	mux.HandleFunc("/api/schedule", x.scheduleAPI)
	mux.HandleFunc("/api/schedule/skip", x.skipScheduleAPI)
	mux.HandleFunc("/api/runs", x.runsAPI)
	mux.HandleFunc("/api/runs/", x.runsAPI)
	mux.HandleFunc("GET /api/classes", x.classesAPI)
	mux.HandleFunc("GET /api/library", x.libraryAPI)
	mux.HandleFunc("GET /api/library/topics/{id}", x.topicAPI)
	mux.HandleFunc("GET /api/library/export", x.exportAPI)
	mux.HandleFunc("POST /api/library/export", x.exportAPI)
	return mux
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (x *app) home(w http.ResponseWriter, r *http.Request) {
	u, in := x.user(r)
	_, csrf := x.session(r)
	data := map[string]any{"In": in, "User": u, "CSRF": csrf, "LoginError": r.URL.Query().Get("error") == "1"}
	if err := page.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render page: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func apiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		apiError(w, 400, "Enter a valid JSON object with the expected fields.")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		apiError(w, 400, "Send one JSON object only.")
		return false
	}
	return true
}
