package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func (x *app) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	users := []User{}
	rows, err := x.db.QueryContext(r.Context(), `SELECT id,email,admin,disabled,pending FROM users ORDER BY id`)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		users = append(users, u)
	}
	if rows.Err() != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "telemetry_enabled": x.telemetryEnabled})
}

type adminLesson struct {
	Class string `json:"class_title"`
	Index int64  `json:"lesson"`
	Words int64  `json:"words_learned"`
}

type adminLessonPlan struct {
	LessonCount        int `json:"lessonCount"`
	ErrorsPer100Words  int `json:"errorsPer100Words"`
	UnmatchedOverrides int `json:"unmatchedOverrides"`
}

type adminClass struct {
	Title    string `json:"title"`
	Override *int   `json:"override"`
}

type adminTelemetry struct {
	LastRunAt        int64            `json:"last_run_at"`
	LastRunStatus    string           `json:"last_run_status"`
	LastLesson       *adminLesson     `json:"last_lesson"`
	ScheduleEnabled  bool             `json:"schedule_enabled"`
	NextRun          int64            `json:"next_run"`
	Classes          []adminClass     `json:"classes"`
	LessonsCompleted int64            `json:"lessons_completed"`
	WordsLearned     int64            `json:"words_learned"`
	Plan             *adminLessonPlan `json:"plan"`
}

func (x *app) adminUserTelemetryAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !x.telemetryEnabled {
		apiError(w, http.StatusForbidden, "User telemetry is disabled.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, http.StatusNotFound, "user not found")
		return
	}
	ctx := r.Context()
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer tx.Rollback()
	var activeAdmin bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND admin=1 AND disabled=0)`, actor.ID).Scan(&activeAdmin)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if !activeAdmin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	var data adminTelemetry
	var classes, config sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(r.status,''),COALESCE(r.created_at,0),
		COALESCE(s.enabled,0),CASE WHEN s.enabled=1 AND u.disabled=0 THEN s.next_run ELSE 0 END,c.data,p.config
		FROM users u
		LEFT JOIN runs r ON r.id=(SELECT id FROM runs WHERE user_id=u.id AND mode='run' ORDER BY id DESC LIMIT 1)
		LEFT JOIN user_schedules s ON s.user_id=u.id LEFT JOIN class_cache c ON c.user_id=u.id
		LEFT JOIN user_settings p ON p.user_id=u.id WHERE u.id=? AND u.pending=0`, id).
		Scan(&data.LastRunStatus, &data.LastRunAt,
			&data.ScheduleEnabled, &data.NextRun, &classes, &config)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	var cached []cachedClass
	if classes.Valid {
		cached, _ = parseCachedClasses(json.RawMessage(classes.String))
	}
	plan := defaultSettings()
	if !config.Valid || json.Unmarshal([]byte(config.String), &plan) == nil {
		var unmatched int
		data.Classes, unmatched = adminClassOverrides(cached, plan.ClassLessonCounts)
		data.Plan = &adminLessonPlan{plan.LessonCount, plan.ErrorsPer100Words, unmatched}
	} else {
		data.Classes, _ = adminClassOverrides(cached, nil)
	}
	if err := data.readLessonTelemetry(ctx, tx, id); err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if err := tx.Commit(); err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func adminClassOverrides(classes []cachedClass, overrides map[string]int) ([]adminClass, int) {
	if classes == nil {
		return nil, len(overrides)
	}
	// Like the worker, prefer a stable ID; a title only applies when unique.
	counts := make(map[string]int, len(overrides))
	for key, count := range overrides {
		counts[strings.ToLower(key)] = count
	}
	titles := make(map[string]int, len(classes))
	for _, class := range classes {
		titles[strings.ToLower(class.Title)]++
	}
	matched := make(map[string]bool)
	result := make([]adminClass, 0, len(classes))
	for _, class := range classes {
		entry := adminClass{Title: class.Title}
		key := "group:" + class.GroupID
		count, ok := counts[key]
		if !ok {
			key = strings.ToLower(class.Title)
			if titles[key] == 1 {
				count, ok = counts[key]
			}
		}
		if ok {
			entry.Override = &count
			matched[key] = true
		}
		result = append(result, entry)
	}
	return result, len(overrides) - len(matched)
}

func (t *adminTelemetry) readLessonTelemetry(ctx context.Context, tx *sql.Tx, userID int64) error {
	// shortcut: read existing results on disclosure, not on every user-list request; index or materialize counts if histories get large.
	err := tx.QueryRowContext(ctx, `WITH results AS (
		SELECT (SELECT data FROM run_events WHERE run_id=r.id
			AND CASE WHEN json_valid(data) THEN data ->> '$.type'='result' END ORDER BY seq DESC LIMIT 1) AS data
		FROM runs r WHERE r.user_id=? AND r.mode='run' AND r.status='completed'
	), counts AS (
		SELECT data ->> '$.lessons_completed' AS lessons, data ->> '$.words_learned' AS words
		FROM results WHERE data -> '$.success'='true'
	) SELECT COALESCE(sum(CASE WHEN typeof(lessons)='integer' AND lessons BETWEEN 0 AND 100000000000 THEN lessons END),0),
		COALESCE(sum(CASE WHEN typeof(words)='integer' AND words BETWEEN 0 AND 100000000000 THEN words END),0) FROM counts`, userID).
		Scan(&t.LessonsCompleted, &t.WordsLearned)
	if err != nil {
		return err
	}
	var latest string
	err = tx.QueryRowContext(ctx, `SELECT e.data FROM runs r JOIN run_events e ON e.run_id=r.id
		WHERE r.user_id=? AND r.mode='run' AND CASE WHEN json_valid(e.data) THEN e.data ->> '$.type'='lesson_complete' END
		ORDER BY r.id DESC,e.seq DESC LIMIT 1`, userID).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var lesson adminLesson
	if err := json.Unmarshal([]byte(latest), &lesson); err == nil && lesson.Class != "" && len(lesson.Class) <= 16<<10 && lesson.Index > 0 && lesson.Words >= 0 {
		t.LastLesson = &lesson
	}
	return nil
}
