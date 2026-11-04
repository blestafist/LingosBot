package webui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const telegramResponseLimit = 4 << 20

type telegramBot struct {
	token    string
	client   *http.Client
	baseURL  string
	mu       sync.RWMutex
	username string
}

func newTelegramBot(token string) *telegramBot {
	return &telegramBot{
		token: token, baseURL: "https://api.telegram.org",
		client: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (b *telegramBot) botUsername() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.username
}

type telegramAPIError struct {
	status     int
	retryAfter time.Duration
}

func (*telegramAPIError) Error() string { return "Telegram request failed" }

func (b *telegramBot) call(ctx context.Context, method string, payload, result any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return &telegramAPIError{}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/bot"+b.token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return &telegramAPIError{}
	}
	r.Header.Set("Content-Type", "application/json")
	// Even an injected client must not forward the token or message to a redirect target.
	client := *b.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// net/http errors contain the full request URL, including the bot token.
		return &telegramAPIError{}
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, telegramResponseLimit+1))
	apiErr := &telegramAPIError{status: response.StatusCode}
	if err != nil || len(raw) > telegramResponseLimit {
		return apiErr
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		ErrorCode  int             `json:"error_code"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return apiErr
	}
	if response.StatusCode != http.StatusOK || !envelope.OK {
		if envelope.ErrorCode >= 400 && envelope.ErrorCode <= 599 {
			apiErr.status = envelope.ErrorCode
		}
		if apiErr.status == http.StatusTooManyRequests {
			apiErr.retryAfter = time.Duration(min(max(envelope.Parameters.RetryAfter, 1), 3600)) * time.Second
		}
		return apiErr
	}
	if result != nil && json.Unmarshal(envelope.Result, result) != nil {
		return &telegramAPIError{}
	}
	return nil
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type telegramMessage struct {
	From *telegramUser `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
}

type telegramUpdate struct {
	ID      int64            `json:"update_id"`
	Message *telegramMessage `json:"message"`
}

func validTelegramUsername(username string) bool {
	if len(username) == 0 || len(username) > 32 {
		return false
	}
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (b *telegramBot) getMe(ctx context.Context) (telegramUser, error) {
	var user telegramUser
	if err := b.call(ctx, "getMe", struct{}{}, &user); err != nil {
		return user, err
	}
	if !user.IsBot || user.ID <= 0 || !validTelegramUsername(user.Username) {
		return user, &telegramAPIError{}
	}
	return user, nil
}

func (x *app) initializeTelegram(ctx context.Context) error {
	user, err := x.telegram.getMe(ctx)
	if err != nil {
		return err
	}
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var botID, offset int64
	if err := tx.QueryRowContext(ctx, `SELECT bot_id,next_update FROM telegram_state WHERE id=1`).Scan(&botID, &offset); err != nil {
		return err
	}
	if botID != 0 && botID != user.ID {
		// A substituted bot has neither the original users' consent nor the same update sequence.
		if _, err := tx.ExecContext(ctx, `DELETE FROM telegram_links; DELETE FROM telegram_tokens; DELETE FROM telegram_outbox`); err != nil {
			return err
		}
		offset = 0
	}
	if _, err := tx.ExecContext(ctx, `UPDATE telegram_state SET bot_id=?,next_update=? WHERE id=1`, user.ID, offset); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	x.telegram.mu.Lock()
	x.telegram.username = user.Username
	x.telegram.mu.Unlock()
	return nil
}

func (b *telegramBot) getUpdates(ctx context.Context, offset int64) ([]telegramUpdate, error) {
	var updates []telegramUpdate
	err := b.call(ctx, "getUpdates", struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		Limit          int      `json:"limit"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{offset, 25, 50, []string{"message"}}, &updates)
	if err != nil {
		return nil, err
	}
	if updates == nil || len(updates) > 50 {
		return nil, &telegramAPIError{}
	}
	for i, update := range updates {
		if update.ID < 0 || update.ID == 1<<63-1 || i > 0 && update.ID <= updates[i-1].ID {
			return nil, &telegramAPIError{}
		}
	}
	return updates, nil
}

func (b *telegramBot) sendMessage(ctx context.Context, chatID int64, text string) error {
	return b.call(ctx, "sendMessage", struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}{chatID, text}, nil)
}

func (x *app) handleTelegramMessage(ctx context.Context, message *telegramMessage) error {
	if message == nil || message.Chat.Type != "private" || message.From == nil || message.From.IsBot || message.Chat.ID <= 0 || message.From.ID != message.Chat.ID || len(message.Text) > 256 {
		return nil
	}
	parts := strings.Fields(message.Text)
	if len(parts) == 0 || len(parts) > 2 {
		return nil
	}
	command := parts[0]
	if name, target, found := strings.Cut(command, "@"); found {
		if x.telegram.botUsername() == "" || !strings.EqualFold(target, x.telegram.botUsername()) {
			return nil
		}
		command = name
	}
	var reply, code string
	switch {
	case command == "/stop" && len(parts) == 1:
		if err := x.unlinkTelegramChat(ctx, message.Chat.ID); err != nil {
			return err
		}
		reply = "Telegram unlinked. Notifications are stopped."
	case (command == "/start" || command == "/help") && len(parts) == 1:
		reply = "Open Telegram settings in LingosBot and use the linking code or link shown there. Use /stop to unlink."
	case command == "/start" && len(parts) == 2:
		code = parts[1]
	case len(parts) == 1:
		code = parts[0]
	default:
		return nil
	}
	if code != "" {
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(code)
		if len(code) != 43 || err != nil || len(decoded) != 32 {
			return nil
		}
		username := message.From.Username
		if !validTelegramUsername(username) {
			username = ""
		}
		firstName := []rune(message.From.FirstName)
		linked, err := x.linkTelegram(ctx, code, message.Chat.ID, username, string(firstName[:min(len(firstName), 64)]))
		if err != nil {
			return err
		}
		reply = "This linking code is invalid or expired. Create a new code in LingosBot."
		if linked {
			reply = "Telegram linked. You can manage notifications in LingosBot."
		}
	}
	if err := x.telegram.sendMessage(ctx, message.Chat.ID, reply); err != nil && ctx.Err() == nil {
		log.Print("Telegram reply unavailable; check bot connectivity and configuration")
	}
	return nil
}

func (x *app) pollTelegramUpdates(ctx context.Context) error {
	var offset int64
	if err := x.db.QueryRowContext(ctx, `SELECT next_update FROM telegram_state WHERE id=1`).Scan(&offset); err != nil {
		return err
	}
	updates, err := x.telegram.getUpdates(ctx, offset)
	if err != nil {
		return err
	}
	for _, update := range updates {
		if update.ID < offset {
			continue
		}
		if err := x.handleTelegramMessage(ctx, update.Message); err != nil {
			return err
		}
		result, err := x.db.ExecContext(ctx, `UPDATE telegram_state SET next_update=? WHERE id=1`, update.ID+1)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("Telegram update state unavailable")
		}
	}
	return nil
}

func telegramWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func (x *app) telegramLoop(ctx context.Context) {
	if x.telegram == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	var notifierDone chan struct{}
	defer func() {
		cancel()
		if notifierDone != nil {
			<-notifierDone
		}
	}()
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		var err error
		if notifierDone == nil {
			err = x.initializeTelegram(ctx)
			if err == nil {
				notifierDone = make(chan struct{})
				go func() {
					defer close(notifierDone)
					x.telegramNotificationLoop(ctx)
				}()
			}
		}
		if err == nil {
			err = x.pollTelegramUpdates(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		delay := 200 * time.Millisecond
		if err != nil {
			log.Print("Telegram polling unavailable; retrying safely")
			delay = backoff
			var apiErr *telegramAPIError
			if errors.As(err, &apiErr) {
				delay = max(delay, apiErr.retryAfter)
			}
			backoff = min(backoff*2, time.Minute)
		} else {
			backoff = 5 * time.Second
		}
		if !telegramWait(ctx, delay) {
			return
		}
	}
}

func telegramNotificationText(run RunRecord) string {
	mode := map[string]string{"run": "Lessons", "classes": "Class sync", "scrape": "Vocabulary", "scrape_all": "All vocabulary"}[run.Mode]
	if mode == "" {
		mode = "Task"
	}
	status := map[string]string{"completed": "Completed", "failed": "Failed", "cancelled": "Cancelled", "interrupted": "Interrupted"}[run.Status]
	source := "manual"
	if run.Source == "schedule" {
		source = "schedule"
	}
	summary := []rune(run.Summary)
	return fmt.Sprintf("LingosBot · %s\nRun #%d · %s\nStatus: %s\n%s", mode, run.ID, source, status, string(summary[:min(len(summary), 1000)]))
}

func (x *app) deliverTelegramNotification(ctx context.Context) (bool, error) {
	var run RunRecord
	var chatID, linkedAt int64
	var attempts int
	var active bool
	err := x.db.QueryRowContext(ctx, `SELECT q.run_id,q.chat_id,q.attempts,r.user_id,r.mode,r.source,r.status,r.summary,
		coalesce(l.linked_at,0),coalesce(l.notifications=1 AND u.disabled=0 AND r.status IN ('completed','failed','cancelled','interrupted'),0)
		FROM telegram_outbox q JOIN runs r ON r.id=q.run_id JOIN users u ON u.id=r.user_id
		LEFT JOIN telegram_links l ON l.user_id=r.user_id AND l.chat_id=q.chat_id
		WHERE q.next_attempt<=? OR l.user_id IS NULL OR l.notifications=0 OR u.disabled=1 OR r.status NOT IN ('completed','failed','cancelled','interrupted')
		ORDER BY q.run_id LIMIT 1`, time.Now().Unix()).Scan(&run.ID, &chatID, &attempts, &run.UserID, &run.Mode, &run.Source, &run.Status, &run.Summary, &linkedAt, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !active {
		_, err = x.db.ExecContext(ctx, `DELETE FROM telegram_outbox WHERE run_id=? AND chat_id=?`, run.ID, chatID)
		return true, err
	}
	// The row scan releases the DB connection before HTTP; unlink/rebind never matches another run owner.
	sendErr := x.telegram.sendMessage(ctx, chatID, telegramNotificationText(run))
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if sendErr == nil {
		// shortcut: a crash after send and before deletion may duplicate a notification; upgrade when Telegram supports idempotent sends.
		_, err = x.db.ExecContext(ctx, `DELETE FROM telegram_outbox WHERE run_id=? AND chat_id=?`, run.ID, chatID)
		return true, err
	}
	var apiErr *telegramAPIError
	if errors.As(sendErr, &apiErr) && (apiErr.status == http.StatusBadRequest || apiErr.status == http.StatusForbidden) {
		tx, err := x.db.BeginTx(ctx, nil)
		if err != nil {
			return true, err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `UPDATE telegram_links SET notifications=0 WHERE user_id=? AND chat_id=? AND linked_at=?
			AND EXISTS(SELECT 1 FROM telegram_outbox WHERE run_id=? AND chat_id=?)`, run.UserID, chatID, linkedAt, run.ID, chatID)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM telegram_outbox WHERE run_id=? AND chat_id=?`, run.ID, chatID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return true, err
		}
	} else if attempts >= 4 {
		_, err = x.db.ExecContext(ctx, `DELETE FROM telegram_outbox WHERE run_id=? AND chat_id=?`, run.ID, chatID)
	} else {
		delay := [...]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}[max(attempts, 0)]
		if apiErr != nil {
			delay = max(delay, apiErr.retryAfter)
		}
		_, err = x.db.ExecContext(ctx, `UPDATE telegram_outbox SET attempts=attempts+1,next_attempt=? WHERE run_id=? AND chat_id=?`, time.Now().Add(delay).Unix(), run.ID, chatID)
	}
	if err != nil {
		return true, err
	}
	return true, sendErr
}

func (x *app) telegramNotificationLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for range 10 {
			processed, err := x.deliverTelegramNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Print("Telegram notification delivery failed")
				var apiErr *telegramAPIError
				if errors.As(err, &apiErr) && apiErr.retryAfter > 0 && !telegramWait(ctx, apiErr.retryAfter) {
					return
				}
				break
			}
			if !processed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
