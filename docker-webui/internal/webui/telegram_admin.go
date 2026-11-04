package webui

import (
	"net/http"
	"strconv"
	"strings"
)

type telegramBinding struct {
	UserID        int64  `json:"user_id"`
	Email         string `json:"email"`
	Disabled      bool   `json:"disabled"`
	Username      string `json:"username"`
	FirstName     string `json:"first_name"`
	ChatID        string `json:"chat_id"`
	Notifications bool   `json:"notifications"`
	LinkedAt      int64  `json:"linked_at"`
}

func (x *app) adminTelegramAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, 403, "forbidden")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/telegram" {
		rows, err := x.db.QueryContext(r.Context(), `SELECT l.user_id,u.email,u.disabled,l.username,l.first_name,l.chat_id,l.notifications,l.linked_at
FROM telegram_links l JOIN users u ON u.id=l.user_id ORDER BY l.user_id`)
		if err != nil {
			apiError(w, 500, "storage error")
			return
		}
		defer rows.Close()
		bindings := []telegramBinding{}
		for rows.Next() {
			var binding telegramBinding
			var chatID int64
			if err := rows.Scan(&binding.UserID, &binding.Email, &binding.Disabled, &binding.Username, &binding.FirstName, &chatID, &binding.Notifications, &binding.LinkedAt); err != nil {
				apiError(w, 500, "storage error")
				return
			}
			binding.ChatID = strconv.FormatInt(chatID, 10)
			bindings = append(bindings, binding)
		}
		if err := rows.Err(); err != nil {
			apiError(w, 500, "storage error")
			return
		}
		writeJSON(w, 200, map[string]any{"bindings": bindings})
		return
	}
	if r.Method != http.MethodDelete {
		apiError(w, 405, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/admin/telegram/"), 10, 64)
	if err != nil || id <= 0 || !strings.HasPrefix(r.URL.Path, "/api/admin/telegram/") {
		apiError(w, 400, "invalid user")
		return
	}
	tx, err := x.db.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, 500, "storage error")
		return
	}
	defer tx.Rollback()
	var activeAdmin bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND admin=1 AND disabled=0)`, actor.ID).Scan(&activeAdmin)
	if err != nil {
		apiError(w, 500, "storage error")
		return
	}
	if !activeAdmin {
		apiError(w, 403, "forbidden")
		return
	}
	if _, err = tx.Exec(`DELETE FROM telegram_links WHERE user_id=?`, id); err == nil {
		_, err = tx.Exec(`DELETE FROM telegram_tokens WHERE user_id=?`, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		apiError(w, 500, "storage error")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
