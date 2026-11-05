package webui

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	registrationAdminOnly = "admin_only"
	registrationApproval  = "approval"
	registrationOpen      = "open"
)

func validRegistrationMode(mode string) bool {
	return mode == registrationAdminOnly || mode == registrationApproval || mode == registrationOpen
}

func (x *app) registrationMode(ctx context.Context) (string, error) {
	var mode string
	err := x.db.QueryRowContext(ctx, `SELECT mode FROM registration_policy WHERE id=1`).Scan(&mode)
	if err == nil && !validRegistrationMode(mode) {
		err = errors.New("invalid registration policy")
	}
	if err != nil {
		return registrationAdminOnly, err
	}
	return mode, nil
}

func (x *app) registrationAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if r.Method == http.MethodPut {
		if !decodeJSON(w, r, &body) {
			return
		}
		if !validRegistrationMode(body.Mode) {
			apiError(w, http.StatusBadRequest, "mode must be admin_only, approval or open")
			return
		}
	}
	tx, err := x.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer tx.Rollback()
	var activeAdmin bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND admin=1 AND disabled=0)`, actor.ID).Scan(&activeAdmin); err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if !activeAdmin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.Method == http.MethodPut {
		err = tx.QueryRow(`UPDATE registration_policy SET mode=? WHERE id=1 RETURNING mode`, body.Mode).Scan(&body.Mode)
	} else {
		err = tx.QueryRow(`SELECT mode FROM registration_policy WHERE id=1`).Scan(&body.Mode)
	}
	if err == nil && !validRegistrationMode(body.Mode) {
		err = errors.New("invalid registration policy")
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (x *app) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !sameOrigin(r) {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// shortcut: emails are unverified; verify ownership before adding email recovery or trusted identity.
	email, valid := normalizeEmail(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	if !valid || !validPassword(password) {
		apiError(w, http.StatusBadRequest, "valid email and password of 12 or more characters and at most 1024 bytes required")
		return
	}
	mode, err := x.registrationMode(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if mode == registrationAdminOnly {
		apiError(w, http.StatusForbidden, "registration is closed")
		return
	}
	if err = x.loginLimit(r, email); err != nil {
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "900")
			apiError(w, http.StatusTooManyRequests, "too many registration attempts")
		} else {
			apiError(w, http.StatusInternalServerError, "storage error")
		}
		return
	}
	// Hash duplicates too: an existing email must not skip the expensive work.
	hash, err := hashPassword(password)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "authentication error")
		return
	}
	tx, err := x.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer tx.Rollback()
	// Read the current policy in the creation transaction, not the pre-hash snapshot.
	if err = tx.QueryRow(`SELECT mode FROM registration_policy WHERE id=1`).Scan(&mode); err != nil || !validRegistrationMode(mode) {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if mode == registrationAdminOnly {
		apiError(w, http.StatusForbidden, "registration is closed")
		return
	}
	pending := mode == registrationApproval
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE email=?)`, email).Scan(&exists); err == nil && !exists {
		var id int64
		err = tx.QueryRow(`UPDATE user_id_counter SET last_id=max(last_id,(SELECT coalesce(max(id),0) FROM users))+1 WHERE id=1 RETURNING last_id`).Scan(&id)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO users(id,email,password_hash,admin,disabled,pending,created_at) VALUES(?,?,?,0,?,?,?)`, id, email, hash, pending, pending, time.Now().Unix())
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"pending": pending})
}
