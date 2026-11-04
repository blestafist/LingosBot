package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type WorkerSettings struct {
	BaseURL               string         `json:"baseUrl"`
	StudentDashboardURL   string         `json:"studentDashboardUrl"`
	LessonCount           int            `json:"lessonCount"`
	ClassLessonCounts     map[string]int `json:"classLessonCounts"`
	LessonPromptSafetyCap int            `json:"lessonPromptSafetyCap"`
	ErrorsPer100Words     int            `json:"errorsPer100Words"`
	AnswerDelayMinMs      int            `json:"answerDelayMinMs"`
	AnswerDelayMaxMs      int            `json:"answerDelayMaxMs"`
	RequestTimeoutSeconds int            `json:"requestTimeoutSeconds"`
	MaxRunSeconds         int            `json:"maxRunSeconds"`
}

func defaultSettings() WorkerSettings {
	return WorkerSettings{
		BaseURL: env("WORKER_BASE_URL", "https://lingos.pl"), StudentDashboardURL: env("WORKER_DASHBOARD_URL", "/student/dashboard"),
		LessonCount: 1, ClassLessonCounts: map[string]int{}, LessonPromptSafetyCap: 30,
		RequestTimeoutSeconds: 30, MaxRunSeconds: 900,
	}
}

func (s WorkerSettings) validate() error {
	if s.LessonCount < 1 || s.LessonCount > 10000 {
		return errors.New("lessonCount must be between 1 and 10000")
	}
	if len(s.ClassLessonCounts) > 10000 {
		return errors.New("classLessonCounts may have at most 10000 entries")
	}
	keys, total := map[string]bool{}, 0
	for name, count := range s.ClassLessonCounts {
		if strings.TrimSpace(name) == "" || count < 0 || count > 10000 {
			return errors.New("classLessonCounts requires nonblank keys and counts between 0 and 10000")
		}
		key := strings.ToLower(name)
		if keys[key] {
			return errors.New("classLessonCounts keys must be unique ignoring case")
		}
		keys[key] = true
		total += count
		if total > 10000 {
			return errors.New("classLessonCounts total must not exceed 10000")
		}
	}
	if s.LessonPromptSafetyCap < 1 || s.LessonPromptSafetyCap > 10000 {
		return errors.New("lessonPromptSafetyCap must be between 1 and 10000")
	}
	if s.ErrorsPer100Words < 0 || s.ErrorsPer100Words > 100 {
		return errors.New("errorsPer100Words must be between 0 and 100")
	}
	if s.AnswerDelayMinMs < 0 || s.AnswerDelayMinMs > s.AnswerDelayMaxMs || s.AnswerDelayMaxMs > 60000 {
		return errors.New("answer delays must satisfy 0 <= answerDelayMinMs <= answerDelayMaxMs <= 60000")
	}
	if s.RequestTimeoutSeconds < 1 || s.RequestTimeoutSeconds > 300 {
		return errors.New("requestTimeoutSeconds must be between 1 and 300")
	}
	if s.MaxRunSeconds < 1 || s.MaxRunSeconds > 86400 {
		return errors.New("maxRunSeconds must be between 1 and 86400")
	}
	return validateWorkerEndpoints(s.BaseURL, s.StudentDashboardURL)
}

func validateWorkerEndpoints(base, dashboard string) error {
	invalidBase := errors.New("baseUrl must be an HTTPS origin (HTTP only on loopback), without credentials, path, query or fragment")
	base = strings.TrimSpace(base)
	if strings.ContainsAny(base, "\\?#") || strings.ContainsFunc(base, unicode.IsControl) {
		return invalidBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.Opaque != "" {
		return invalidBase
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback())) {
		return invalidBase
	}
	invalidDashboard := errors.New("studentDashboardUrl must be a path or URL on the baseUrl origin, without credentials, query or fragment")
	dashboard = strings.TrimSpace(dashboard)
	if dashboard == "" || strings.ContainsAny(dashboard, "\\?#") || strings.ContainsFunc(dashboard, unicode.IsControl) {
		return invalidDashboard
	}
	d, err := url.Parse(dashboard)
	if err != nil || d.User != nil || d.Opaque != "" {
		return invalidDashboard
	}
	d = u.ResolveReference(d)
	port := func(v *url.URL) string {
		if v.Port() != "" {
			return v.Port()
		}
		if v.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	if d.Scheme != u.Scheme || !strings.EqualFold(d.Hostname(), u.Hostname()) || port(d) != port(u) {
		return invalidDashboard
	}
	return nil
}

func (x *app) loadSettings(userID int64) (WorkerSettings, error) {
	s := defaultSettings()
	var config string
	err := x.db.QueryRow(`SELECT config FROM user_settings WHERE user_id=?`, userID).Scan(&config)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(config), &s); err != nil {
			return s, err
		}
	}
	// Endpoints are deployment policy, never trust an old/user-written database value.
	s.BaseURL, s.StudentDashboardURL = env("WORKER_BASE_URL", "https://lingos.pl"), env("WORKER_DASHBOARD_URL", "/student/dashboard")
	if s.ClassLessonCounts == nil {
		s.ClassLessonCounts = map[string]int{}
	}
	return s, s.validate()
}

func workerExecutionLimit() (int, error) {
	limit, err := strconv.Atoi(env("WORKER_MAX_RUN_SECONDS", "3600"))
	if err != nil || limit < 1 || limit > 86400 {
		return 0, errors.New("WORKER_MAX_RUN_SECONDS must be between 1 and 86400")
	}
	return limit, nil
}

func (x *app) settingsAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var s WorkerSettings
	if r.Method == http.MethodPut {
		s = defaultSettings()
		if !decodeJSON(w, r, &s) {
			return
		}
		defaults := defaultSettings()
		if s.BaseURL != defaults.BaseURL || s.StudentDashboardURL != defaults.StudentDashboardURL {
			apiError(w, http.StatusBadRequest, "worker endpoints are managed by the administrator")
			return
		}
		if err := s.validate(); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.ClassLessonCounts == nil {
			s.ClassLessonCounts = map[string]int{}
		}
		config, err := json.Marshal(s)
		if err == nil {
			_, err = x.db.Exec(`INSERT INTO user_settings(user_id,config) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET config=excluded.config`, u.ID, string(config))
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "settings storage error")
			return
		}
	} else {
		var err error
		s, err = x.loadSettings(u.ID)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "settings storage error")
			return
		}
	}
	limit, err := workerExecutionLimit()
	if err != nil {
		apiError(w, 500, "Invalid server execution limit.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s, "baseUrl": s.BaseURL, "studentDashboardUrl": s.StudentDashboardURL, "endpointsManaged": true, "executionLimitSeconds": limit})
}

// Call after bootstrapAdmin so legacy singleton data can be assigned to user 1.
func initSettingsDB(db *sql.DB) error {
	if err := defaultSettings().validate(); err != nil {
		return fmt.Errorf("worker defaults: %w", err)
	}
	zone := env("TZ", "UTC")
	if zone == "Local" {
		return errors.New("TZ must be an explicit IANA timezone, not Local")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return fmt.Errorf("TZ: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS user_settings (
		user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, config TEXT NOT NULL
	); CREATE TABLE IF NOT EXISTS user_schedules (
		user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		enabled INTEGER NOT NULL DEFAULT 0, time TEXT NOT NULL, timezone TEXT NOT NULL,
		days TEXT NOT NULL, cron TEXT NOT NULL DEFAULT '', next_run INTEGER NOT NULL DEFAULT 0,
		last_run INTEGER NOT NULL DEFAULT 0, last_run_id INTEGER NOT NULL DEFAULT 0,
		last_status TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1, lease_until INTEGER NOT NULL DEFAULT 0
	); CREATE INDEX IF NOT EXISTS user_schedules_due ON user_schedules(enabled,next_run);
	CREATE TABLE IF NOT EXISTS settings_meta (key TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schedule'
		AND NOT EXISTS(SELECT 1 FROM settings_meta WHERE key='legacy_schedule_v1')`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		var s Schedule
		var lessons, mistakes int
		var lastDate string
		err := tx.QueryRow(`SELECT enabled,time,lessons,errors,last_date FROM schedule WHERE id=1`).Scan(&s.Enabled, &s.Time, &lessons, &mistakes, &lastDate)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			settings := defaultSettings()
			settings.LessonCount, settings.ErrorsPer100Words = lessons, mistakes
			if err := settings.validate(); err != nil {
				return fmt.Errorf("legacy settings: %w", err)
			}
			config, err := json.Marshal(settings)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO user_settings(user_id,config) VALUES(1,?)`, string(config)); err != nil {
				return err
			}
			s.Timezone, s.Days = zone, []int{0, 1, 2, 3, 4, 5, 6}
			parsed, err := s.parse()
			if err != nil {
				return fmt.Errorf("legacy schedule: %w", err)
			}
			now := time.Now().In(loc)
			if lastDate != "" {
				last, err := time.ParseInLocation("2006-01-02 15:04", lastDate+" "+s.Time, loc)
				if err != nil {
					return fmt.Errorf("legacy schedule last_date: %w", err)
				}
				s.LastRun, s.LastStatus = last.Unix(), "legacy"
				if last.After(now) {
					now = last
				}
			}
			if s.Enabled {
				// The old scheduler claimed whole local dates, including the repeated DST hour.
				candidate := parsed.Next(now)
				for !candidate.IsZero() && candidate.In(loc).Format("2006-01-02") == lastDate {
					candidate = parsed.Next(candidate)
				}
				if candidate.IsZero() {
					return errors.New("legacy schedule has no next occurrence")
				}
				s.NextRun = candidate.Unix()
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO user_schedules(user_id,enabled,time,timezone,days,next_run,last_run,last_status) VALUES(1,?,?,?,?,?,?,?)`, s.Enabled, s.Time, s.Timezone, `[0,1,2,3,4,5,6]`, s.NextRun, s.LastRun, s.LastStatus); err != nil {
				return err
			}
		}
		// initDB can recreate the legacy singleton; mark migration separately to never resurrect it.
		if _, err := tx.Exec(`INSERT INTO settings_meta(key) VALUES('legacy_schedule_v1')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
