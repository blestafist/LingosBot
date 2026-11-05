package webui

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func initTelegramDB(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS telegram_links (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 chat_id INTEGER NOT NULL UNIQUE CHECK(chat_id>0), username TEXT NOT NULL, first_name TEXT NOT NULL,
 notifications INTEGER NOT NULL DEFAULT 0 CHECK(notifications IN (0,1)), linked_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS telegram_tokens (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE, expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS telegram_state (id INTEGER PRIMARY KEY CHECK(id=1), next_update INTEGER NOT NULL DEFAULT 0, bot_id INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO telegram_state(id) VALUES(1);
CREATE TABLE IF NOT EXISTS telegram_outbox (
 run_id INTEGER PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE, chat_id INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS telegram_outbox_due ON telegram_outbox(next_attempt);
DROP TRIGGER IF EXISTS telegram_run_finished;
CREATE TRIGGER telegram_run_finished AFTER UPDATE OF status ON runs
WHEN OLD.status='running' AND NEW.mode='run' AND NEW.status IN ('completed','failed','cancelled','interrupted')
BEGIN
 INSERT OR IGNORE INTO telegram_outbox(run_id,chat_id)
 SELECT NEW.id,l.chat_id FROM telegram_links l JOIN users u ON u.id=l.user_id
 WHERE l.user_id=NEW.user_id AND l.notifications=1 AND u.disabled=0;
END;
DELETE FROM telegram_outbox WHERE run_id IN (SELECT id FROM runs WHERE mode<>'run');
CREATE TRIGGER IF NOT EXISTS telegram_unlinked AFTER DELETE ON telegram_links
BEGIN
 DELETE FROM telegram_outbox WHERE run_id IN (SELECT id FROM runs WHERE user_id=OLD.user_id);
 DELETE FROM telegram_tokens WHERE user_id=OLD.user_id;
END;
CREATE TRIGGER IF NOT EXISTS telegram_muted AFTER UPDATE OF notifications ON telegram_links WHEN NEW.notifications=0
BEGIN
 DELETE FROM telegram_outbox WHERE run_id IN (SELECT id FROM runs WHERE user_id=NEW.user_id);
END;
CREATE TRIGGER IF NOT EXISTS telegram_account_changed AFTER UPDATE OF password_hash,disabled ON users
WHEN NEW.disabled=1 OR OLD.password_hash<>NEW.password_hash
BEGIN
 DELETE FROM telegram_tokens WHERE user_id=NEW.id;
 DELETE FROM telegram_outbox WHERE NEW.disabled=1 AND run_id IN (SELECT id FROM runs WHERE user_id=NEW.id);
END;`)
	return err
}

type telegramStatus struct {
	Configured    bool   `json:"configured"`
	BotUsername   string `json:"bot_username"`
	Linked        bool   `json:"linked"`
	Username      string `json:"username"`
	FirstName     string `json:"first_name"`
	ChatID        string `json:"chat_id"`
	Notifications bool   `json:"notifications"`
	LinkedAt      int64  `json:"linked_at"`
	CodeExpiresAt int64  `json:"code_expires_at"`
}

func (x *app) telegramAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		state := telegramStatus{Configured: x.telegram != nil}
		if x.telegram != nil {
			state.BotUsername = x.telegram.botUsername()
		}
		var chatID int64
		err := x.db.QueryRowContext(r.Context(), `SELECT chat_id,username,first_name,notifications,linked_at FROM telegram_links WHERE user_id=?`, u.ID).
			Scan(&chatID, &state.Username, &state.FirstName, &state.Notifications, &state.LinkedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, "storage error")
			return
		}
		state.Linked = err == nil
		if state.Linked {
			state.ChatID = strconv.FormatInt(chatID, 10)
		}
		err = x.db.QueryRowContext(r.Context(), `SELECT expires FROM telegram_tokens WHERE user_id=? AND expires>?`, u.ID, time.Now().Unix()).Scan(&state.CodeExpiresAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, "storage error")
			return
		}
		writeJSON(w, 200, state)
	case http.MethodPatch:
		var body struct {
			Notifications *bool `json:"notifications"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Notifications == nil {
			apiError(w, 400, "Choose whether to receive run notifications.")
			return
		}
		result, err := x.db.ExecContext(r.Context(), `UPDATE telegram_links SET notifications=? WHERE user_id=? AND EXISTS(SELECT 1 FROM users WHERE id=? AND disabled=0)`, *body.Notifications, u.ID, u.ID)
		if err != nil {
			apiError(w, 500, "storage error")
			return
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			apiError(w, 409, "Connect Telegram first.")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case http.MethodDelete:
		if err := x.unlinkTelegram(r.Context(), u.ID); err != nil {
			apiError(w, 500, "storage error")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		apiError(w, 405, "method not allowed")
	}
}

func (x *app) telegramLinkAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, 405, "method not allowed")
		return
	}
	if x.telegram == nil || x.telegram.botUsername() == "" {
		apiError(w, 503, "Telegram is not available yet. Ask your administrator to check the bot configuration.")
		return
	}
	code, err := randomToken()
	if err != nil {
		apiError(w, 500, "Could not create a link code.")
		return
	}
	expires := time.Now().Add(10 * time.Minute).Unix()
	tx, err := x.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, 500, "storage error")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM telegram_tokens WHERE expires<=?`, time.Now().Unix())
	var result sql.Result
	if err == nil {
		result, err = tx.Exec(`INSERT INTO telegram_tokens(user_id,token_hash,expires)
SELECT id,?,? FROM users WHERE id=? AND disabled=0 AND NOT EXISTS(SELECT 1 FROM telegram_links WHERE user_id=?)
ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires=excluded.expires`, tokenHash(code), expires, u.ID, u.ID)
	}
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			apiError(w, 409, "Disconnect your existing Telegram account before creating another link.")
			return
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, 500, "storage error")
		return
	}
	writeJSON(w, 201, map[string]any{"code": code, "expires_at": expires, "bot_username": x.telegram.botUsername()})
}

// Codes grant only a Telegram binding, never a website session or access to Lingos credentials.
func (x *app) linkTelegram(ctx context.Context, code string, chatID int64, username, firstName string) (bool, error) {
	if len(code) != 43 || chatID <= 0 || len(username) > 64 || len(firstName) > 256 {
		return false, nil
	}
	if raw, err := base64.RawURLEncoding.DecodeString(code); err != nil || len(raw) != 32 {
		return false, nil
	}
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var userID int64
	err = tx.QueryRow(`SELECT t.user_id FROM telegram_tokens t JOIN users u ON u.id=t.user_id
WHERE t.token_hash=? AND t.expires>? AND u.disabled=0
AND NOT EXISTS(SELECT 1 FROM telegram_links WHERE user_id=t.user_id OR chat_id=?)`, tokenHash(code), time.Now().Unix(), chatID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO telegram_links(user_id,chat_id,username,first_name,linked_at) VALUES(?,?,?,?,?)`, userID, chatID, username, firstName, time.Now().Unix())
	if err == nil {
		_, err = tx.Exec(`DELETE FROM telegram_tokens WHERE user_id=?`, userID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err == nil, err
}

func (x *app) unlinkTelegram(ctx context.Context, userID int64) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM telegram_links WHERE user_id=?`, userID); err == nil {
		_, err = tx.Exec(`DELETE FROM telegram_tokens WHERE user_id=?`, userID)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}

func (x *app) unlinkTelegramChat(ctx context.Context, chatID int64) error {
	_, err := x.db.ExecContext(ctx, `DELETE FROM telegram_links WHERE chat_id=?`, chatID)
	return err
}
