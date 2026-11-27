package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAdminRegistrationRequests(t *testing.T) {
	x := integrationApp(t)
	setRegistrationTestMode(t, x, registrationApproval)
	admin, csrf := authTestSession(t, x, 1)
	member, _ := authTestSession(t, x, authTestUser(t, x, "member@example.com", false))
	handler := x.routes().ServeHTTP
	const path = "/api/admin/registration/requests"
	for token, want := range map[string]int{"": http.StatusUnauthorized, member: http.StatusForbidden} {
		if w := authTestRequest(handler, "GET", path, "", token, ""); w.Code != want {
			t.Fatalf("got status %d, want %d", w.Code, want)
		}
	}
	type listedRequest struct {
		ID        int64
		Email     string
		CreatedAt int64 `json:"created_at"`
	}
	list := func() []listedRequest {
		t.Helper()
		w := authTestRequest(handler, "GET", path, "", admin, "")
		var data struct{ Requests []listedRequest }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Requests == nil {
			t.Fatalf("unexpected queue: %d %s", w.Code, w.Body.String())
		}
		return data.Requests
	}
	if len(list()) != 0 {
		t.Fatal("active users appeared in the queue")
	}
	const email = "applicant@example.com"
	for range 2 {
		if w := registrationTestRequest(x, email, authTestPassword); w.Code != http.StatusCreated {
			t.Fatalf("signup: %d %s", w.Code, w.Body.String())
		}
	}
	setRegistrationTestMode(t, x, registrationAdminOnly)
	requests := list()
	if len(requests) != 1 || requests[0].Email != email || requests[0].CreatedAt <= 0 {
		t.Fatalf("unexpected pending requests after mode change: %+v", requests)
	}
	userPath := fmt.Sprintf("/api/admin/users/%d", requests[0].ID)
	if w := authTestRequest(handler, "PATCH", userPath, `{"approve":true}`, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if len(list()) != 0 {
		t.Fatal("approved user remains in the queue")
	}
}
