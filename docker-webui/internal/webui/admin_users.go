package webui

import "net/http"

type adminLessonRun struct {
	Source     string `json:"source"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	FinishedAt int64  `json:"finished_at"`
}

type adminUser struct {
	User
	CreatedAt       int64           `json:"created_at"`
	LastLessonRun   *adminLessonRun `json:"last_lesson_run"`
	ScheduleEnabled bool            `json:"schedule_enabled"`
	NextRun         int64           `json:"next_run"`
}

func (x *app) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	// Keep worker output, Lingos credentials and other private account data out of this response.
	rows, err := x.db.QueryContext(r.Context(), `SELECT u.id,u.email,u.admin,u.disabled,u.pending,u.created_at,
		COALESCE(r.source,''),COALESCE(r.status,''),COALESCE(r.created_at,0),COALESCE(r.finished_at,0),
		COALESCE(s.enabled,0),CASE WHEN s.enabled=1 AND u.disabled=0 THEN s.next_run ELSE 0 END
		FROM users u
		LEFT JOIN runs r ON r.id=(SELECT id FROM runs WHERE user_id=u.id AND mode='run' ORDER BY id DESC LIMIT 1)
		LEFT JOIN user_schedules s ON s.user_id=u.id ORDER BY u.id`)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer rows.Close()
	users := []adminUser{}
	for rows.Next() {
		var u adminUser
		var run adminLessonRun
		if err := rows.Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending, &u.CreatedAt,
			&run.Source, &run.Status, &run.CreatedAt, &run.FinishedAt, &u.ScheduleEnabled, &u.NextRun); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		if run.Status != "" {
			u.LastLessonRun = &run
		}
		users = append(users, u)
	}
	if rows.Err() != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}
