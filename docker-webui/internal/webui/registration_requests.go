package webui

import "net/http"

func (x *app) registrationRequestsAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	type registrationRequest struct {
		ID        int64  `json:"id"`
		Email     string `json:"email"`
		CreatedAt int64  `json:"created_at"`
	}
	rows, err := x.db.QueryContext(r.Context(), `SELECT id,email,created_at FROM users WHERE pending=1 ORDER BY id`)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer rows.Close()
	requests := []registrationRequest{}
	for rows.Next() {
		var req registrationRequest
		if err := rows.Scan(&req.ID, &req.Email, &req.CreatedAt); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		requests = append(requests, req)
	}
	if rows.Err() != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
}
