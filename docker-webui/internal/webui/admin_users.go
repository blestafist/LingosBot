package webui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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

type adminLessonRun struct {
	Source     string `json:"source"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	FinishedAt int64  `json:"finished_at"`
}

type adminLesson struct {
	Class string `json:"class_title"`
	Index int64  `json:"lesson"`
	Words int64  `json:"words_learned"`
}

// Only named lesson-plan values leave the instance. Override names are user-entered
// text and do not belong in telemetry, nor do endpoints or any other saved config.
type adminLessonPlan struct {
	LessonCount           int `json:"lessonCount"`
	LessonPromptSafetyCap int `json:"lessonPromptSafetyCap"`
	ErrorsPer100Words     int `json:"errorsPer100Words"`
	AnswerDelayMinMs      int `json:"answerDelayMinMs"`
	AnswerDelayMaxMs      int `json:"answerDelayMaxMs"`
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds"`
	MaxRunSeconds         int `json:"maxRunSeconds"`
	ClassOverrides        int `json:"classOverrides"`
}

type adminTelemetry struct {
	CreatedAt        int64            `json:"created_at"`
	LastLessonRun    *adminLessonRun  `json:"last_lesson_run"`
	LastLesson       *adminLesson     `json:"last_lesson"`
	ScheduleEnabled  bool             `json:"schedule_enabled"`
	NextRun          int64            `json:"next_run"`
	Classes          []string         `json:"classes"`
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
	var run adminLessonRun
	var classes, config sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT u.created_at,
		COALESCE(r.source,''),COALESCE(r.status,''),COALESCE(r.created_at,0),COALESCE(r.finished_at,0),
		COALESCE(s.enabled,0),CASE WHEN s.enabled=1 AND u.disabled=0 THEN s.next_run ELSE 0 END,c.data,p.config
		FROM users u
		LEFT JOIN runs r ON r.id=(SELECT id FROM runs WHERE user_id=u.id AND mode='run' ORDER BY id DESC LIMIT 1)
		LEFT JOIN user_schedules s ON s.user_id=u.id LEFT JOIN class_cache c ON c.user_id=u.id
		LEFT JOIN user_settings p ON p.user_id=u.id WHERE u.id=? AND u.pending=0`, id).
		Scan(&data.CreatedAt, &run.Source, &run.Status, &run.CreatedAt, &run.FinishedAt,
			&data.ScheduleEnabled, &data.NextRun, &classes, &config)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if run.Status != "" {
		data.LastLessonRun = &run
	}
	if classes.Valid {
		if cached, err := parseCachedClasses(json.RawMessage(classes.String)); err == nil {
			data.Classes = make([]string, 0, len(cached))
			for _, class := range cached {
				data.Classes = append(data.Classes, class.Title)
			}
		}
	}
	plan := defaultSettings()
	if !config.Valid || json.Unmarshal([]byte(config.String), &plan) == nil {
		data.Plan = &adminLessonPlan{
			LessonCount: plan.LessonCount, LessonPromptSafetyCap: plan.LessonPromptSafetyCap,
			ErrorsPer100Words: plan.ErrorsPer100Words, AnswerDelayMinMs: plan.AnswerDelayMinMs,
			AnswerDelayMaxMs: plan.AnswerDelayMaxMs, RequestTimeoutSeconds: plan.RequestTimeoutSeconds,
			MaxRunSeconds: plan.MaxRunSeconds, ClassOverrides: len(plan.ClassLessonCounts),
		}
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
