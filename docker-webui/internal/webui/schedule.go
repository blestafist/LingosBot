package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

type Schedule struct {
	Enabled    bool   `json:"enabled"`
	Time       string `json:"time"`
	Timezone   string `json:"timezone"`
	Days       []int  `json:"days"`
	Cron       string `json:"cron"`
	NextRun    int64  `json:"nextRun"`
	LastRun    int64  `json:"lastRun"`
	LastRunID  int64  `json:"lastRunId"`
	LastStatus string `json:"lastStatus"`
	LastError  string `json:"lastError"`
	version    int64
}

func defaultSchedule() Schedule {
	return Schedule{Time: "09:00", Timezone: env("TZ", "UTC"), Days: []int{1, 2, 3, 4, 5}}
}

func (s Schedule) parse() (cron.Schedule, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if s.Timezone == "" || s.Timezone == "Local" || err != nil {
		return nil, errors.New("timezone must be an explicit IANA timezone, for example Europe/Warsaw")
	}
	t, err := time.Parse("15:04", s.Time)
	if err != nil || t.Format("15:04") != s.Time {
		return nil, errors.New("time must use HH:MM (24-hour)")
	}
	if len(s.Days) == 0 || len(s.Days) > 7 {
		return nil, errors.New("days must contain weekdays 0 (Sunday) through 6 (Saturday)")
	}
	days, seen := make([]string, 0, len(s.Days)), map[int]bool{}
	for _, day := range s.Days {
		if day < 0 || day > 6 || seen[day] {
			return nil, errors.New("days must contain unique weekdays 0 (Sunday) through 6 (Saturday)")
		}
		seen[day] = true
		days = append(days, strconv.Itoa(day))
	}
	expression := strings.TrimSpace(s.Cron)
	if expression == "" {
		expression = fmt.Sprintf("%d %d * * %s", t.Minute(), t.Hour(), strings.Join(days, ","))
	} else if len(strings.Fields(expression)) != 5 {
		return nil, errors.New("cron must contain exactly five fields: minute hour day-of-month month day-of-week")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(expression)
	if err != nil {
		return nil, errors.New("invalid five-field cron expression")
	}
	// The parser must not inherit time.Local or accept user-supplied CRON_TZ directives.
	parsed.(*cron.SpecSchedule).Location = loc
	return parsed, nil
}

func (x *app) loadSchedule(userID int64) (Schedule, error) {
	s := defaultSchedule()
	var days string
	err := x.db.QueryRow(`SELECT enabled,time,timezone,days,cron,next_run,last_run,last_run_id,last_status,last_error,version FROM user_schedules WHERE user_id=?`, userID).
		Scan(&s.Enabled, &s.Time, &s.Timezone, &days, &s.Cron, &s.NextRun, &s.LastRun, &s.LastRunID, &s.LastStatus, &s.LastError, &s.version)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(days), &s.Days); err != nil {
		return s, err
	}
	if s.LastRunID != 0 && s.LastStatus != "waiting" && s.LastStatus != "skipped" {
		var status, summary string
		err := x.db.QueryRow(`SELECT status,summary FROM runs WHERE id=? AND user_id=?`, s.LastRunID, userID).Scan(&status, &summary)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return s, err
		}
		if err == nil {
			s.LastStatus = status
			if status == "failed" || status == "interrupted" {
				s.LastError = summary
			}
		}
	}
	return s, nil
}

func (x *app) scheduleAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		s := defaultSchedule()
		if !decodeJSON(w, r, &s) {
			return
		}
		parsed, err := s.parse()
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		now, next := time.Now(), int64(0)
		if s.Enabled {
			candidate := parsed.Next(now)
			if candidate.IsZero() {
				apiError(w, http.StatusBadRequest, "schedule has no occurrence within the next five years")
				return
			}
			next = candidate.Unix()
		}
		sort.Ints(s.Days)
		days, _ := json.Marshal(s.Days)
		result, err := x.db.Exec(`INSERT INTO user_schedules(user_id,enabled,time,timezone,days,cron,next_run)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET
			enabled=excluded.enabled,time=excluded.time,timezone=excluded.timezone,days=excluded.days,
			cron=excluded.cron,next_run=excluded.next_run,version=user_schedules.version+1,
			last_status=CASE WHEN user_schedules.last_status IN ('waiting','skipped') THEN '' ELSE user_schedules.last_status END,
			last_error='',lease_until=0 WHERE user_schedules.lease_until<=?`,
			u.ID, s.Enabled, s.Time, s.Timezone, string(days), strings.TrimSpace(s.Cron), next, now.Unix())
		if err != nil {
			apiError(w, http.StatusInternalServerError, "schedule storage error")
			return
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			apiError(w, http.StatusConflict, "schedule is being started; retry saving in a moment")
			return
		}
	default:
		w.Header().Set("Allow", "GET, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s, err := x.loadSchedule(u.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "schedule storage error")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (x *app) skipScheduleAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in struct {
		NextRun int64 `json:"nextRun"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.NextRun <= 0 {
		apiError(w, http.StatusBadRequest, "nextRun must be a positive Unix timestamp")
		return
	}
	s, err := x.loadSchedule(u.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "schedule storage error")
		return
	}
	if !s.Enabled || s.NextRun <= 0 || s.NextRun != in.NextRun {
		apiError(w, http.StatusConflict, "schedule changed; reload before skipping")
		return
	}
	parsed, err := s.parse()
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	next := parsed.Next(time.Unix(max(now.Unix(), s.NextRun), 0))
	if next.IsZero() {
		apiError(w, http.StatusBadRequest, "schedule has no occurrence within the next five years")
		return
	}
	// A durable run can outlive its lease after a crash; it is already too late to skip it.
	result, err := x.db.Exec(`UPDATE user_schedules SET next_run=?,last_status='skipped',last_error='',
		lease_until=0,version=version+1 WHERE user_id=? AND enabled=1 AND next_run=? AND version=? AND lease_until<=?
		AND NOT EXISTS(SELECT 1 FROM runs WHERE user_id=? AND scheduled_for=?)`,
		next.Unix(), u.ID, in.NextRun, s.version, now.Unix(), u.ID, in.NextRun)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "schedule storage error")
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "schedule storage error")
		return
	}
	if n != 1 {
		apiError(w, http.StatusConflict, "schedule changed or is being started; reload before skipping")
		return
	}
	s, err = x.loadSchedule(u.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "schedule storage error")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (x *app) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		x.runScheduled(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (x *app) runScheduled(now time.Time) {
	rows, err := x.db.Query(`SELECT s.user_id,s.next_run,s.version,s.time,s.timezone,s.days,s.cron
		FROM user_schedules s JOIN users u ON u.id=s.user_id
		WHERE s.enabled=1 AND u.disabled=0 AND s.next_run>0 AND s.next_run<=? AND s.lease_until<=?
		ORDER BY s.next_run,s.user_id`, now.Unix(), now.Unix())
	if err != nil {
		log.Printf("schedule: load: %v", err)
		return
	}
	type dueSchedule struct {
		userID int64
		s      Schedule
	}
	var due []dueSchedule
	for rows.Next() {
		var d dueSchedule
		var days string
		if err = rows.Scan(&d.userID, &d.s.NextRun, &d.s.version, &d.s.Time, &d.s.Timezone, &days, &d.s.Cron); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(days), &d.s.Days); err != nil {
			break
		}
		due = append(due, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Printf("schedule: read: %v", err)
		return
	}
	for _, d := range due {
		s := d.s
		parsed, err := s.parse()
		if err != nil {
			log.Printf("schedule: user %d: invalid saved schedule", d.userID)
			continue
		}
		// A short durable lease avoids concurrent starters; runs' unique slot survives expired leases/crashes.
		lease := now.Unix() + 60
		result, err := x.db.Exec(`UPDATE user_schedules SET lease_until=? WHERE user_id=? AND enabled=1
			AND next_run=? AND version=? AND lease_until<=? AND EXISTS(SELECT 1 FROM users WHERE id=? AND disabled=0)`,
			lease, d.userID, s.NextRun, s.version, now.Unix(), d.userID)
		if err != nil {
			log.Printf("schedule: claim: %v", err)
			continue
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			continue
		}
		runID, startErr := x.startScheduledRun(d.userID, s.NextRun)
		if startErr != nil {
			message := "Unable to start worker; check saved Lingos credentials and server configuration. Will retry."
			if errors.Is(startErr, errWorkerBusy) {
				message = "Worker is busy; will retry."
			} else if errors.Is(startErr, errCredentials) {
				message = "Save Lingos credentials to start this schedule; will retry."
			}
			_, err = x.db.Exec(`UPDATE user_schedules SET lease_until=0,last_status='waiting',last_error=?
				WHERE user_id=? AND next_run=? AND version=? AND lease_until=?`, message, d.userID, s.NextRun, s.version, lease)
		} else {
			// Skip missed occurrences: one durable run, then the first occurrence after this poll.
			next := parsed.Next(now)
			var nextUnix int64
			if !next.IsZero() {
				nextUnix = next.Unix()
			}
			_, err = x.db.Exec(`UPDATE user_schedules SET lease_until=0,next_run=?,
				last_run=COALESCE((SELECT created_at FROM runs WHERE id=? AND user_id=?),?),
				last_run_id=?,last_status='started',last_error=''
				WHERE user_id=? AND next_run=? AND version=? AND lease_until=?`, nextUnix, runID, d.userID, now.Unix(), runID, d.userID, s.NextRun, s.version, lease)
		}
		if err != nil {
			log.Printf("schedule: save result: %v", err)
		}
	}
}
