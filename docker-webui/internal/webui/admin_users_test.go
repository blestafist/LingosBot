package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAdminUserTelemetry(t *testing.T) {
	x := integrationApp(t)
	x.telemetryEnabled = true
	member := authTestUser(t, x, "member@example.com", false)
	disabled := authTestUser(t, x, "disabled@example.com", false)
	pending := authTestUser(t, x, "pending@example.com", false)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE users SET disabled=1 WHERE id=?`, []any{disabled}},
		{`UPDATE users SET disabled=1,pending=1 WHERE id=?`, []any{pending}},
		{`INSERT INTO user_schedules(user_id,enabled,time,timezone,days,next_run) VALUES
			(?,1,'09:00','UTC','[1]',500),(?,1,'09:00','UTC','[1]',600)`, []any{member, disabled}},
		{`INSERT INTO runs(id,user_id,mode,class_id,source,status,summary,created_at,finished_at) VALUES
			(1,?,'run','','manual','completed','private output',100,110),
			(2,?,'run','','schedule','completed','private output',200,220),
			(3,?,'run','','manual','failed','private output',300,320),
			(4,?,'classes','','manual','completed','private output',400,420),
			(5,?,'run','','manual','completed','private output',500,520)`, []any{member, member, member, member, disabled}},
		{`INSERT INTO run_events(run_id,seq,data) VALUES
			(1,1,'{"type":"lesson_complete","class_title":"Polish","lesson":1,"words_learned":4}'),
			(1,2,'{"type":"result","success":true,"lessons_completed":1,"words_learned":4}'),
			(2,1,'{"type":"lesson_complete","class_title":"Spanish","lesson":2,"words_learned":8,"word":"private prompt"}'),
			(2,2,'{"type":"result","success":true,"lessons_completed":2,"words_learned":12,"username":"private username"}'),
			(2,3,'invalid legacy event'),
			(3,1,'{"type":"result","success":true,"lessons_completed":50,"words_learned":50}'),
			(4,1,'{"type":"result","success":true,"lessons_completed":60,"words_learned":60}'),
			(5,1,'{"type":"result","success":true,"lessons_completed":70,"words_learned":70}')`, nil},
		{`INSERT INTO class_cache(user_id,data) VALUES(?, '[{"title":"Polish","group_id":"7","secret":"private class data"},{"title":"Spanish","group_id":"8"}]')`, []any{member}},
		{`INSERT INTO user_settings(user_id,config) VALUES(?, '{"lessonCount":2,"baseUrl":"https://private.example","classLessonCounts":{"private override":3},"credentials":{"email":"private config"}}')`, []any{member}},
		{`INSERT INTO user_credentials(user_id,email,password) VALUES(?, 'hidden@lingos.example', X'ABCD')`, []any{member}},
	} {
		if _, err := x.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	handler := x.routes().ServeHTTP
	adminToken, adminCSRF := authTestSession(t, x, 1)
	memberToken, _ := authTestSession(t, x, member)
	list := "/api/admin/users"
	path := func(id int64) string { return fmt.Sprintf("%s/%d/telemetry", list, id) }
	for _, check := range []struct {
		method, path, body, token, csrf string
		status                          int
	}{
		{"GET", list, "", "", "", 401},
		{"GET", list, "", memberToken, "", 403},
		{"GET", path(member), "", "", "", 401},
		{"GET", path(member), "", memberToken, "", 403},
		{"GET", path(pending), "", adminToken, "", 404},
		{"PUT", "/api/admin/instance-settings", `{"telemetry_enabled":false}`, adminToken, adminCSRF, 404},
	} {
		if w := authTestRequest(handler, check.method, check.path, check.body, check.token, check.csrf); w.Code != check.status {
			t.Fatalf("%s %s: got %d, want %d: %s", check.method, check.path, w.Code, check.status, w.Body.String())
		}
	}
	checkList := func(enabled bool) {
		t.Helper()
		w := authTestRequest(handler, "GET", list, "", adminToken, "")
		var result struct {
			TelemetryEnabled *bool `json:"telemetry_enabled"`
			Users            []map[string]json.RawMessage
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Users) != 4 || result.TelemetryEnabled == nil || *result.TelemetryEnabled != enabled {
			t.Fatalf("unexpected user list: %d %s", w.Code, w.Body.String())
		}
		fields := result.Users[1]
		if len(fields) != 5 {
			t.Fatalf("unexpected user fields: %v", fields)
		}
		for _, key := range []string{"id", "email", "admin", "disabled", "pending"} {
			if fields[key] == nil {
				t.Errorf("missing field %q", key)
			}
		}
	}
	checkList(true)
	w := authTestRequest(handler, "GET", path(member), "", adminToken, "")
	var data adminTelemetry
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatalf("telemetry unavailable: %d %s", w.Code, w.Body.String())
	}
	if data.CreatedAt <= 0 || !data.ScheduleEnabled || data.NextRun != 500 || data.LessonsCompleted != 3 || data.WordsLearned != 16 ||
		!reflect.DeepEqual(data.Classes, []string{"Polish", "Spanish"}) || data.Plan == nil || data.Plan.LessonCount != 2 || data.Plan.ClassOverrides != 1 {
		t.Fatalf("unexpected telemetry: %+v", data)
	}
	if want := (adminLessonRun{"manual", "failed", 300, 320}); data.LastLessonRun == nil || *data.LastLessonRun != want {
		t.Fatalf("unexpected latest run: %+v", data.LastLessonRun)
	}
	if want := (adminLesson{"Spanish", 2, 8}); data.LastLesson == nil || *data.LastLesson != want {
		t.Fatalf("unexpected last completed lesson: %+v", data.LastLesson)
	}
	for _, private := range []string{"private", "hidden@lingos.example", "summary", "password", "group_id", "baseUrl", "studentDashboardUrl", "classLessonCounts"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("telemetry contains %q: %s", private, w.Body.String())
		}
	}
	w = authTestRequest(handler, "GET", path(disabled), "", adminToken, "")
	data = adminTelemetry{}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.NextRun != 0 || data.Classes != nil || data.LastLesson != nil || data.Plan == nil || data.Plan.LessonCount != 1 {
		t.Fatalf("unexpected telemetry for disabled user: %d %s", w.Code, w.Body.String())
	}
	x.telemetryEnabled = false
	checkList(false)
	if w := authTestRequest(handler, "GET", path(member), "", adminToken, ""); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Spanish") {
		t.Fatalf("disabled telemetry exposed: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminTelemetryEnv(t *testing.T) {
	for _, tc := range []struct {
		value         string
		want, invalid bool
	}{
		{"", false, false}, {"0", false, false}, {"1", true, false}, {"true", true, false}, {"false", false, false}, {"typo", false, true},
	} {
		t.Setenv("ADMIN_TELEMETRY", tc.value)
		if got, err := adminTelemetryEnabled(); got != tc.want || (err != nil) != tc.invalid {
			t.Errorf("ADMIN_TELEMETRY=%q: got %v, %v", tc.value, got, err)
		}
	}
}
