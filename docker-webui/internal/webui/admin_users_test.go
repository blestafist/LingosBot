package webui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestAdminUsersLessonTelemetry(t *testing.T) {
	x := integrationApp(t)
	member := authTestUser(t, x, "member@example.com", false)
	paused := authTestUser(t, x, "paused@example.com", false)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE users SET disabled=1 WHERE id=?`, []any{paused}},
		{`INSERT INTO user_schedules(user_id,enabled,time,timezone,days,next_run) VALUES
			(?,1,'09:00','UTC','[1]',500),(?,1,'09:00','UTC','[1]',600)`, []any{member, paused}},
		{`INSERT INTO runs(user_id,mode,class_id,source,status,summary,created_at,finished_at) VALUES
			(?,'run','','manual','failed','old output',100,110),
			(?,'run','','schedule','completed','private worker output',200,220),
			(?,'classes','','manual','completed','private class list',300,310)`, []any{member, member, member}},
		{`INSERT INTO user_credentials(user_id,email,password) VALUES(?, 'hidden@lingos.example', X'ABCD')`, []any{member}},
	} {
		if _, err := x.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/admin/users"
	handler := x.routes().ServeHTTP
	memberToken, _ := authTestSession(t, x, member)
	for token, want := range map[string]int{"": http.StatusUnauthorized, memberToken: http.StatusForbidden} {
		if w := authTestRequest(handler, "GET", path, "", token, ""); w.Code != want {
			t.Fatalf("got status %d, want %d", w.Code, want)
		}
	}
	adminToken, _ := authTestSession(t, x, 1)
	w := authTestRequest(handler, "GET", path, "", adminToken, "")
	var result struct{ Users []adminUser }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Users) != 3 {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
	u := result.Users[1]
	wantRun := &adminLessonRun{Source: "schedule", Status: "completed", CreatedAt: 200, FinishedAt: 220}
	if u.ID != member || u.CreatedAt <= 0 || !reflect.DeepEqual(u.LastLessonRun, wantRun) || !u.ScheduleEnabled || u.NextRun != 500 {
		t.Fatalf("unexpected telemetry: %+v, run: %+v", u, u.LastLessonRun)
	}
	if u := result.Users[2]; u.ID != paused || u.LastLessonRun != nil || !u.Disabled || u.NextRun != 0 {
		t.Fatalf("unexpected disabled account telemetry: %+v", u)
	}
	// Lock down the response shape, including nested telemetry, rather than only checking known secret names.
	var raw struct{ Users []map[string]json.RawMessage }
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	fields := raw.Users[1]
	if len(fields) != 9 {
		t.Fatalf("unexpected user fields: %v", fields)
	}
	for _, key := range []string{"id", "email", "admin", "disabled", "pending", "created_at", "last_lesson_run", "schedule_enabled", "next_run"} {
		if fields[key] == nil {
			t.Errorf("missing field %q", key)
		}
	}
	var runFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["last_lesson_run"], &runFields); err != nil || len(runFields) != 4 {
		t.Fatalf("unexpected run fields: %s", fields["last_lesson_run"])
	}
}
