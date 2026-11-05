package webui

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

type User struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Admin    bool   `json:"admin"`
	Disabled bool   `json:"disabled"`
	Pending  bool   `json:"pending"`
}

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 254 {
		return email, false
	}
	address, err := mail.ParseAddress(email)
	return email, err == nil && address.Address == email
}

func (x *app) sessionUser(r *http.Request) (User, string, string, error) {
	var u User
	cookie, err := r.Cookie("session")
	if err != nil || len(cookie.Value) != 43 {
		return u, "", "", sql.ErrNoRows
	}
	var csrf string
	err = x.db.QueryRowContext(r.Context(), `SELECT u.id,u.email,u.admin,u.disabled,u.pending,s.csrf FROM user_sessions s JOIN users u ON u.id=s.user_id WHERE s.token=? AND s.expires>? AND u.disabled=0`, tokenHash(cookie.Value), time.Now().Unix()).Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending, &csrf)
	return u, cookie.Value, csrf, err
}

func (x *app) session(r *http.Request) (token, csrf string) {
	_, token, csrf, err := x.sessionUser(r)
	if err != nil {
		return "", ""
	}
	return token, csrf
}

func (x *app) user(r *http.Request) (User, bool) {
	u, _, _, err := x.sessionUser(r)
	return u, err == nil
}

func (x *app) apiUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, _, csrf, err := x.sessionUser(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, http.StatusUnauthorized, "unauthorized")
		} else {
			apiError(w, http.StatusInternalServerError, "storage error")
		}
		return User{}, false
	}
	if r.Method != http.MethodGet {
		supplied := r.Header.Get("X-CSRF-Token")
		if supplied == "" && r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil {
				apiError(w, http.StatusBadRequest, "invalid request")
				return User{}, false
			}
			supplied = r.PostForm.Get("csrf")
		}
		if !sameOrigin(r) || csrf == "" || subtle.ConstantTimeCompare([]byte(supplied), []byte(csrf)) != 1 {
			apiError(w, http.StatusForbidden, "forbidden")
			return User{}, false
		}
	}
	return u, true
}

func sessionCookie(value string, age int) *http.Cookie {
	return &http.Cookie{Name: "session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: cookieSecure(), MaxAge: age}
}

func (x *app) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	email := strings.ToLower(strings.TrimSpace(r.PostForm.Get("email")))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(r.PostForm.Get("user")))
	}
	password := r.PostForm.Get("password")
	if len(email) > 254 || len(password) > 1024 {
		apiError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := x.loginLimit(r, email); err != nil {
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "900")
			apiError(w, http.StatusTooManyRequests, "too many login attempts")
		} else {
			apiError(w, http.StatusInternalServerError, "storage error")
		}
		return
	}
	var id int64
	var hash []byte
	var disabled bool
	err := x.db.QueryRowContext(r.Context(), `SELECT id,password_hash,disabled FROM users WHERE email=?`, email).Scan(&id, &hash, &disabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if !checkPassword(hash, password) || disabled || err != nil {
		apiError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, err := randomToken()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "authentication error")
		return
	}
	csrf, err := randomToken()
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
	if _, err = tx.Exec(`DELETE FROM user_sessions WHERE token IN (SELECT token FROM user_sessions WHERE expires<=? LIMIT 100)`, time.Now().Unix()); err == nil {
		// Recheck the hash and disabled flag in the write transaction: password resets must win races with login.
		var result sql.Result
		result, err = tx.Exec(`INSERT INTO user_sessions(token,csrf,user_id,expires) SELECT ?,?,id,? FROM users WHERE id=? AND password_hash=? AND disabled=0`, tokenHash(token), csrf, time.Now().Add(12*time.Hour).Unix(), id, hash)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				apiError(w, http.StatusUnauthorized, "invalid email or password")
				return
			}
		}
	}
	if err == nil {
		// Refund only this successful attempt; earlier failures must keep their budget.
		ipKey, emailKey := loginLimitKeys(r, email)
		_, err = tx.Exec(`UPDATE auth_limits SET attempts=attempts-1 WHERE key IN (?,?) AND attempts>0`, ipKey, emailKey)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	http.SetCookie(w, sessionCookie(token, 12*60*60))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (x *app) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := x.apiUser(w, r); !ok {
		return
	}
	cookie, _ := r.Cookie("session")
	if _, err := x.db.ExecContext(r.Context(), `DELETE FROM user_sessions WHERE token=?`, tokenHash(cookie.Value)); err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	http.SetCookie(w, sessionCookie("", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (x *app) accountAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"user": u})
	case http.MethodPatch:
		var body struct {
			CurrentPassword string `json:"current_password"`
			Password        string `json:"password"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if !validPassword(body.Password) || len(body.CurrentPassword) > 1024 {
			apiError(w, http.StatusBadRequest, "password must be 12 or more characters and at most 1024 bytes")
			return
		}
		if err := x.loginLimit(r, u.Email); err != nil {
			if errors.Is(err, errRateLimited) {
				apiError(w, http.StatusTooManyRequests, "too many attempts")
			} else {
				apiError(w, http.StatusInternalServerError, "storage error")
			}
			return
		}
		tx, err := x.db.BeginTx(r.Context(), nil)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		defer tx.Rollback()
		var hash []byte
		if err = tx.QueryRow(`SELECT password_hash FROM users WHERE id=? AND disabled=0`, u.ID).Scan(&hash); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		if !checkPassword(hash, body.CurrentPassword) {
			apiError(w, http.StatusUnauthorized, "invalid password")
			return
		}
		hash, err = hashPassword(body.Password)
		if err == nil {
			_, err = tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, u.ID)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM user_sessions WHERE user_id=?`, u.ID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		http.SetCookie(w, sessionCookie("", -1))
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (x *app) credentialsAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		var email string
		var verifiedAt int64
		err := x.db.QueryRowContext(r.Context(), `SELECT email,verified_at FROM user_credentials WHERE user_id=?`, u.ID).Scan(&email, &verifiedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": err == nil, "email": email, "verified_at": verifiedAt})
	case http.MethodPut:
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		email, valid := normalizeEmail(body.Email)
		if !valid || body.Password == "" || len(body.Password) > 1024 {
			apiError(w, http.StatusBadRequest, "valid email and password required")
			return
		}
		encrypted, err := x.encryptCredentials(u.ID, body.Password)
		if err == nil {
			_, err = x.db.ExecContext(r.Context(), `INSERT INTO user_credentials(user_id,email,password) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,password=excluded.password`, u.ID, email, encrypted)
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "email": email, "verified_at": 0})
	case http.MethodDelete:
		if _, err := x.db.ExecContext(r.Context(), `DELETE FROM user_credentials WHERE user_id=?`, u.ID); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": false, "email": "", "verified_at": 0})
	default:
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (x *app) adminUsersAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	if r.URL.Path == "/api/admin/users" && r.Method == http.MethodGet {
		rows, err := x.db.QueryContext(r.Context(), `SELECT id,email,admin,disabled,pending FROM users ORDER BY id`)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		defer rows.Close()
		users := []User{}
		for rows.Next() {
			var u User
			if err = rows.Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending); err != nil {
				apiError(w, http.StatusInternalServerError, "storage error")
				return
			}
			users = append(users, u)
		}
		if rows.Err() != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Email    *string `json:"email"`
		Password *string `json:"password"`
		Admin    *bool   `json:"admin"`
		Disabled *bool   `json:"disabled"`
		Approve  *bool   `json:"approve"`
	}
	if r.Method != http.MethodDelete && !decodeJSON(w, r, &body) {
		return
	}
	if body.Approve != nil && (r.Method != http.MethodPatch || !*body.Approve || body.Admin != nil || body.Disabled != nil || body.Password != nil) {
		apiError(w, http.StatusBadRequest, "approve must be true and sent without other changes")
		return
	}
	var u User
	if r.Method == http.MethodPost {
		var valid bool
		if body.Email != nil {
			u.Email, valid = normalizeEmail(*body.Email)
		}
		if r.URL.Path != "/api/admin/users" || !valid || body.Password == nil || body.Disabled != nil {
			apiError(w, http.StatusBadRequest, "valid email and password required")
			return
		}
	} else {
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), 10, 64)
		if err != nil || id <= 0 || !strings.HasPrefix(r.URL.Path, "/api/admin/users/") || body.Email != nil {
			apiError(w, http.StatusBadRequest, "invalid user")
			return
		}
		u.ID = id
	}
	var hash []byte
	var err error
	if body.Password != nil {
		if !validPassword(*body.Password) {
			apiError(w, http.StatusBadRequest, "password must be 12 or more characters and at most 1024 bytes")
			return
		}
		hash, err = hashPassword(*body.Password)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "authentication error")
			return
		}
	}
	if r.Method == http.MethodDelete {
		// Match launchRun's lock order so no worker can start between the busy check and deletion.
		x.runMu.Lock()
		defer x.runMu.Unlock()
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
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var exists bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE email=?)`, u.Email).Scan(&exists); err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		if exists {
			apiError(w, http.StatusConflict, "email already exists")
			return
		}
		u.Admin = body.Admin != nil && *body.Admin
		err = tx.QueryRow(`UPDATE user_id_counter SET last_id=max(last_id,(SELECT coalesce(max(id),0) FROM users))+1 WHERE id=1 RETURNING last_id`).Scan(&u.ID)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO users(id,email,password_hash,admin,created_at) VALUES(?,?,?,?,?)`, u.ID, u.Email, hash, u.Admin, time.Now().Unix())
		}
		status = http.StatusCreated
	} else {
		err = tx.QueryRow(`SELECT id,email,admin,disabled,pending FROM users WHERE id=?`, u.ID).Scan(&u.ID, &u.Email, &u.Admin, &u.Disabled, &u.Pending)
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			apiError(w, http.StatusInternalServerError, "storage error")
			return
		}
		if r.Method == http.MethodDelete && r.URL.Query().Get("pending") == "1" && !u.Pending {
			apiError(w, http.StatusConflict, "registration request is no longer pending")
			return
		}
		if body.Approve != nil {
			if !u.Pending {
				apiError(w, http.StatusConflict, "user is not pending approval")
				return
			}
			u.Pending, u.Disabled, u.Admin = false, false, false
		} else if u.Pending && ((body.Disabled != nil && !*body.Disabled) || (body.Admin != nil && *body.Admin)) {
			apiError(w, http.StatusConflict, "approve the pending user first")
			return
		}
		if body.Admin != nil {
			u.Admin = *body.Admin
		}
		if body.Disabled != nil {
			u.Disabled = *body.Disabled
		}
		if r.Method == http.MethodDelete || !u.Admin || u.Disabled {
			var otherAdmins int
			if err = tx.QueryRow(`SELECT count(*) FROM users WHERE admin=1 AND disabled=0 AND id<>?`, u.ID).Scan(&otherAdmins); err != nil {
				apiError(w, http.StatusInternalServerError, "storage error")
				return
			}
			if otherAdmins == 0 {
				apiError(w, http.StatusConflict, "cannot delete, disable or demote the last active admin")
				return
			}
		}
		if r.Method == http.MethodDelete {
			var busy bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE user_id=? AND status='running')`, u.ID).Scan(&busy)
			// A worker can still be finishing after its run record becomes terminal.
			for id := range x.cancels {
				if err != nil || busy {
					break
				}
				err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE id=? AND user_id=?)`, id, u.ID).Scan(&busy)
			}
			if err != nil {
				apiError(w, http.StatusInternalServerError, "storage error")
				return
			}
			if busy {
				apiError(w, http.StatusConflict, "user has an active run; wait for it to finish")
				return
			}
			for _, query := range []string{
				`UPDATE user_id_counter SET last_id=max(last_id,?) WHERE id=1`,
				`DELETE FROM class_cache WHERE user_id=?`,
				`DELETE FROM library_classes WHERE user_id=?`,
				`DELETE FROM runs WHERE user_id=?`,
				`DELETE FROM users WHERE id=?`,
			} {
				if _, err = tx.Exec(query, u.ID); err != nil {
					break
				}
			}
			if err == nil && u.ID == 1 {
				_, err = tx.Exec(`DELETE FROM credentials WHERE id=1; DELETE FROM schedule WHERE id=1`)
			}
		} else {
			_, err = tx.Exec(`UPDATE users SET admin=?,disabled=?,pending=? WHERE id=?`, u.Admin, u.Disabled, u.Pending, u.ID)
			if err == nil && body.Password != nil {
				_, err = tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, u.ID)
			}
			if err == nil && (u.Disabled || body.Password != nil || body.Approve != nil) {
				_, err = tx.Exec(`DELETE FROM user_sessions WHERE user_id=?`, u.ID)
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if r.Method == http.MethodDelete {
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}
	if u.Disabled {
		if err := x.cancelUserRuns(u.ID); err != nil {
			apiError(w, http.StatusInternalServerError, "could not stop user runs")
			return
		}
	}
	writeJSON(w, status, u)
}
