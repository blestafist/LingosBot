package webui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
)

type schedule struct {
	Enabled bool
	Time    string
	Lessons int
	Errors  int
}

func initDB(db *sql.DB) error {
	db.SetMaxOpenConns(1)
	_, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE IF NOT EXISTS credentials (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, password BLOB NOT NULL); CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, csrf TEXT NOT NULL, expires INTEGER NOT NULL); CREATE TABLE IF NOT EXISTS schedule (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL, time TEXT NOT NULL, lessons INTEGER NOT NULL, errors INTEGER NOT NULL, last_date TEXT NOT NULL DEFAULT ''); INSERT OR IGNORE INTO schedule(id,enabled,time,lessons,errors) VALUES(1,0,'09:00',1,0)`)
	return err
}

func (x *app) loadSchedule() (schedule, error) {
	var s schedule
	err := x.db.QueryRow(`SELECT enabled,time,lessons,errors FROM schedule WHERE id=1`).Scan(&s.Enabled, &s.Time, &s.Lessons, &s.Errors)
	return s, err
}

func (x *app) saveSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !x.authorize(w, r) {
		return
	}
	t := r.FormValue("time")
	parsed, err := time.Parse("15:04", t)
	lessons, lessonsErr := strconv.Atoi(r.FormValue("lessons"))
	errorsPer100, errorsErr := strconv.Atoi(r.FormValue("errors"))
	if err != nil || parsed.Format("15:04") != t || lessonsErr != nil || errorsErr != nil || lessons < 1 || lessons > 10000 || errorsPer100 < 0 || errorsPer100 > 100 {
		http.Error(w, "invalid schedule settings", http.StatusBadRequest)
		return
	}
	_, err = x.db.Exec(`UPDATE schedule SET enabled=?,time=?,lessons=?,errors=? WHERE id=1`, r.FormValue("enabled") != "", t, lessons, errorsPer100)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?schedule_saved=1#schedule", http.StatusSeeOther)
}

func (x *app) workerConfig(lessons, errorsPer100 int) ([]byte, error) {
	var username string
	var encrypted []byte
	if err := x.db.QueryRow(`SELECT username,password FROM credentials WHERE id=1`).Scan(&username, &encrypted); err != nil {
		return nil, err
	}
	n := x.aead.NonceSize()
	if len(encrypted) < n {
		return nil, fmt.Errorf("invalid encrypted credentials")
	}
	password, err := x.aead.Open(nil, encrypted[:n], encrypted[n:], nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(creds{Username: username, Password: string(password), LessonCount: lessons, ErrorsPer100Words: errorsPer100})
}

func (x *app) scheduleLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		x.runScheduled(time.Now())
		<-ticker.C
	}
}

func (x *app) runScheduled(now time.Time) {
	s, err := x.loadSchedule()
	if err != nil {
		log.Printf("schedule: load: %v", err)
		return
	}
	if !s.Enabled || s.Time != now.Format("15:04") {
		return
	}
	// Claim before starting, so restarts and the repeated DST hour cannot run twice.
	result, err := x.db.Exec(`UPDATE schedule SET last_date=? WHERE id=1 AND enabled=1 AND time=? AND lessons=? AND errors=? AND last_date<>?`, now.Format("2006-01-02"), s.Time, s.Lessons, s.Errors, now.Format("2006-01-02"))
	if err != nil {
		log.Printf("schedule: claim: %v", err)
		return
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return
	}
	if !x.workerMu.TryLock() {
		log.Print("schedule: skipped; worker already running")
		return
	}
	go func() {
		defer x.workerMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := x.runScheduledWorker(ctx, s)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Print("schedule: skipped; save Lingos credentials first")
			} else if ctx.Err() != nil {
				log.Print("schedule: worker timed out")
			} else {
				log.Printf("schedule: worker failed: %v", err)
			}
		} else {
			log.Print("schedule: worker completed")
		}
	}()
}

func (x *app) runScheduledWorker(ctx context.Context, s schedule) error {
	cfg, err := x.workerConfig(s.Lessons, s.Errors)
	if err != nil {
		return err
	}
	args, _ := workerArgs("run", "")
	cmd := exec.CommandContext(ctx, x.worker, args...)
	cmd.Stdin = bytes.NewReader(cfg)
	return cmd.Run()
}
