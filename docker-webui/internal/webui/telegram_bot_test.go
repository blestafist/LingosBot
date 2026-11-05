package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const transportTestToken = "123456:never-log-this-token"

func transportTestBot(t *testing.T, handler http.HandlerFunc) *telegramBot {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	bot := newTelegramBot(transportTestToken)
	bot.baseURL = server.URL
	return bot
}

func transportTestApp(t *testing.T, handler http.HandlerFunc) *app {
	t.Helper()
	x := newAuthTestApp(t)
	if err := initTelegramDB(x.db); err != nil {
		t.Fatal(err)
	}
	x.telegram = transportTestBot(t, handler)
	return x
}

func transportTestCode(t *testing.T, x *app, userID int64) string {
	t.Helper()
	code, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`INSERT INTO telegram_tokens(user_id,token_hash,expires) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires=excluded.expires`, userID, tokenHash(code), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	return code
}

func transportTestMessage(text string, chatID int64) *telegramMessage {
	m := &telegramMessage{From: &telegramUser{ID: chatID, Username: "learner", FirstName: "Learner"}, Text: text}
	m.Chat.ID, m.Chat.Type = chatID, "private"
	return m
}

func transportTestLink(t *testing.T, x *app, userID, chatID int64, enabled bool) {
	t.Helper()
	if _, err := x.db.Exec(`INSERT INTO telegram_links(user_id,chat_id,username,first_name,notifications,linked_at) VALUES(?,?,'learner','Learner',?,?)`, userID, chatID, enabled, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func transportTestRun(t *testing.T, x *app, userID int64, mode, status, source string) int64 {
	t.Helper()
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,summary,created_at) VALUES(?,?,'',?,'running','',0)`, userID, mode, source)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`UPDATE runs SET status=?,summary='Backend summary' WHERE id=?`, status, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func transportTestQueueCount(t *testing.T, x *app) int {
	t.Helper()
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestTelegramTransportRequests(t *testing.T) {
	var methods []string
	bot := transportTestBot(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request shape: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+transportTestToken+"/")
		methods = append(methods, method)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch method {
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"Lingos_TestBot"}}`)
		case "getUpdates":
			if body["offset"] != float64(42) || body["timeout"] != float64(25) || body["limit"] != float64(50) || !reflect.DeepEqual(body["allowed_updates"], []any{"message"}) {
				t.Errorf("unexpected getUpdates body: %v", body)
			}
			io.WriteString(w, `{"ok":true,"result":[]}`)
		case "sendMessage":
			if len(body) != 2 || body["chat_id"] != float64(99) || body["text"] != "plain <text>" {
				t.Errorf("unexpected sendMessage body: %v", body)
			}
			io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected Telegram method")
		}
	})
	if bot.client.Timeout != 40*time.Second {
		t.Fatal("HTTP timeout must exceed Telegram long polling timeout")
	}
	if user, err := bot.getMe(context.Background()); err != nil || user.Username != "Lingos_TestBot" {
		t.Fatalf("getMe: %s %v", user.Username, err)
	}
	if updates, err := bot.getUpdates(context.Background(), 42); err != nil || len(updates) != 0 {
		t.Fatalf("getUpdates: %v %v", updates, err)
	}
	if err := bot.sendMessage(context.Background(), 99, "plain <text>"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"getMe", "getUpdates", "sendMessage"}) {
		t.Fatal(methods)
	}
}

type telegramFailingTransport struct{}

func (telegramFailingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("secret request %s", r.URL.String())
}

func TestTelegramTransportSanitizesFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		retry      time.Duration
	}{
		{"api-error", `{"ok":false,"error_code":403,"description":"` + transportTestToken + `"}`, 403, 0},
		{"rate-limit", `{"ok":false,"error_code":429,"parameters":{"retry_after":123}}`, 429, 123 * time.Second},
		{"rate-limit-cap", `{"ok":false,"error_code":429,"parameters":{"retry_after":9223372036854775807}}`, 429, time.Hour},
		{"invalid-json", transportTestToken, 502, 0},
		{"oversized", `{"ok":true,"result":{},"padding":"` + strings.Repeat("a", telegramResponseLimit) + `"}`, 200, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			bot := transportTestBot(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			})
			_, err := bot.getMe(context.Background())
			var apiErr *telegramAPIError
			if !errors.As(err, &apiErr) || apiErr.status != test.status || apiErr.retryAfter != test.retry || strings.Contains(fmt.Sprintf("%+v", err), transportTestToken) {
				t.Fatalf("unsafe or unexpected error: %v", err)
			}
		})
	}
	bot := newTelegramBot(transportTestToken)
	bot.client.Transport = telegramFailingTransport{}
	if _, err := bot.getMe(context.Background()); err == nil || strings.Contains(err.Error(), transportTestToken) || strings.Contains(err.Error(), "https://") {
		t.Fatalf("transport leaked URL: %v", err)
	}
	bot.baseURL = ":invalid"
	if _, err := bot.getMe(context.Background()); err == nil || strings.Contains(err.Error(), transportTestToken) {
		t.Fatal("invalid URL was not sanitized")
	}
}

func TestTelegramTransportRejectsRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	bot := transportTestBot(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	bot.client.CheckRedirect = nil
	err := bot.sendMessage(context.Background(), 1, "private text")
	var apiErr *telegramAPIError
	if !errors.As(err, &apiErr) || apiErr.status != 307 || redirected.Load() != 0 {
		t.Fatalf("redirect followed: %v requests=%d", err, redirected.Load())
	}
}

func TestTelegramGetMeValidation(t *testing.T) {
	for _, body := range []string{
		`{"id":1,"is_bot":false,"username":"valid"}`,
		`{"id":0,"is_bot":true,"username":"valid"}`,
		`{"id":1,"is_bot":true,"username":"bad/name"}`,
		`{"id":1,"is_bot":true,"username":""}`,
		`{"id":1,"is_bot":true,"username":"` + strings.Repeat("a", 33) + `"}`,
		`null`,
	} {
		bot := transportTestBot(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"ok":true,"result":%s}`, body) })
		if _, err := bot.getMe(context.Background()); err == nil || bot.botUsername() != "" {
			t.Fatal("invalid bot identity accepted")
		}
	}
}

func TestTelegramIdentityRetainsOrResetsBindings(t *testing.T) {
	for _, previousID := range []int64{0, 2, 1} {
		t.Run(fmt.Sprint(previousID), func(t *testing.T) {
			x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"ok":true,"result":{"id":2,"is_bot":true,"username":"Lingos_NewBot"}}`)
			})
			transportTestLink(t, x, 1, 123, true)
			transportTestRun(t, x, 1, "run", "completed", "manual")
			transportTestCode(t, x, 1)
			if _, err := x.db.Exec(`UPDATE telegram_state SET bot_id=?,next_update=42`, previousID); err != nil {
				t.Fatal(err)
			}
			if err := x.initializeTelegram(context.Background()); err != nil {
				t.Fatal(err)
			}
			var botID, offset, links, codes int64
			if err := x.db.QueryRow(`SELECT bot_id,next_update,(SELECT count(*) FROM telegram_links),(SELECT count(*) FROM telegram_tokens) FROM telegram_state`).Scan(&botID, &offset, &links, &codes); err != nil {
				t.Fatal(err)
			}
			wantOffset, wantBindings := int64(42), int64(1)
			if previousID == 1 {
				wantOffset, wantBindings = 0, 0
			}
			if botID != 2 || offset != wantOffset || links != wantBindings || codes != wantBindings || transportTestQueueCount(t, x) != int(wantBindings) || x.telegram.botUsername() != "Lingos_NewBot" {
				t.Fatalf("identity transition retained wrong state: bot=%d offset=%d links=%d codes=%d", botID, offset, links, codes)
			}
		})
	}
}

func TestTelegramIdentityFailureDoesNotPublishUsername(t *testing.T) {
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"result":{"id":2,"is_bot":true,"username":"Lingos_NewBot"}}`)
	})
	transportTestLink(t, x, 1, 123, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	if _, err := x.db.Exec(`UPDATE telegram_state SET bot_id=1,next_update=42; CREATE TRIGGER reject_telegram_identity BEFORE UPDATE ON telegram_state BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := x.initializeTelegram(context.Background()); err == nil || x.telegram.botUsername() != "" || transportTestQueueCount(t, x) != 1 {
		t.Fatalf("failed identity transaction published availability or discarded queue: %v", err)
	}
	var links, botID, offset int
	if err := x.db.QueryRow(`SELECT bot_id,next_update,(SELECT count(*) FROM telegram_links) FROM telegram_state`).Scan(&botID, &offset, &links); err != nil || botID != 1 || offset != 42 || links != 1 {
		t.Fatalf("identity reset did not roll back: bot=%d offset=%d links=%d err=%v", botID, offset, links, err)
	}
}

func TestTelegramPrivateLinkAndStop(t *testing.T) {
	var replies []string
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Text string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		replies = append(replies, body.Text)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	x.telegram.username = "Lingos_TestBot"
	for _, prefix := range []string{"/start@lingos_testbot ", ""} {
		code := transportTestCode(t, x, 1)
		if err := x.handleTelegramMessage(context.Background(), transportTestMessage(prefix+code, 123)); err != nil {
			t.Fatal(err)
		}
		var chatID int64
		var username, firstName string
		if err := x.db.QueryRow(`SELECT chat_id,username,first_name FROM telegram_links WHERE user_id=1`).Scan(&chatID, &username, &firstName); err != nil || chatID != 123 || username != "learner" || firstName != "Learner" {
			t.Fatalf("link was not stored: %d %s %s %v", chatID, username, firstName, err)
		}
		if _, err := x.db.Exec(`UPDATE telegram_links SET notifications=1`); err != nil {
			t.Fatal(err)
		}
		transportTestRun(t, x, 1, "run", "completed", "manual")
		transportTestCode(t, x, 1)
		if err := x.handleTelegramMessage(context.Background(), transportTestMessage("/stop", 123)); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM telegram_links)+(SELECT count(*) FROM telegram_tokens)+(SELECT count(*) FROM telegram_outbox)`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stop retained private data: count=%d err=%v", count, err)
		}
	}
	if err := x.handleTelegramMessage(context.Background(), transportTestMessage("/start", 123)); err != nil || len(replies) != 5 {
		t.Fatalf("help reply missing: %d %v", len(replies), err)
	}
	if err := x.handleTelegramMessage(context.Background(), transportTestMessage("/help", 123)); err != nil || len(replies) != 6 || replies[4] != replies[5] {
		t.Fatalf("/help differs from /start: %d %v", len(replies), err)
	}
	for _, reply := range replies {
		if strings.Contains(reply, "admin") || strings.Contains(reply, transportTestToken) || strings.Contains(reply, "@") {
			t.Fatal("reply exposes account details")
		}
	}
}

func TestTelegramIgnoresUnsafeMessages(t *testing.T) {
	var sent atomic.Int32
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	code := transportTestCode(t, x, 1)
	for _, kind := range []string{"group", "supergroup", "channel", "bot", "other-sender", "negative", "no-sender", "garbage", "wrong-target", "malformed-code", "oversized"} {
		m := transportTestMessage("/start "+code, 123)
		switch kind {
		case "group", "supergroup", "channel":
			m.Chat.Type = kind
		case "bot":
			m.From.IsBot = true
		case "other-sender":
			m.From.ID++
		case "negative":
			m.From.ID, m.Chat.ID = -123, -123
		case "no-sender":
			m.From = nil
		case "garbage":
			m.Text = "hello arbitrary input"
		case "wrong-target":
			m.Text = "/start@another_bot " + code
		case "malformed-code":
			m.Text = strings.Repeat("!", 43)
		case "oversized":
			m.Text = strings.Repeat("a", 257)
		}
		if err := x.handleTelegramMessage(context.Background(), m); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_links`).Scan(&count); err != nil || count != 0 || sent.Load() != 0 {
		t.Fatalf("unsafe message processed: links=%d sends=%d err=%v", count, sent.Load(), err)
	}
}

func TestTelegramPollPersistsOffsetAndStorageFailure(t *testing.T) {
	var offset int64
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Offset int64 }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		offset = body.Offset
		io.WriteString(w, `{"ok":true,"result":[{"update_id":40},{"update_id":41}]}`)
	})
	if err := x.pollTelegramUpdates(context.Background()); err != nil {
		t.Fatal(err)
	}
	var saved int64
	if err := x.db.QueryRow(`SELECT next_update FROM telegram_state`).Scan(&saved); err != nil || saved != 42 || offset != 0 {
		t.Fatalf("offset not durable: %d %d %v", saved, offset, err)
	}
	if err := x.pollTelegramUpdates(context.Background()); err != nil || offset != 42 {
		t.Fatalf("persisted offset not reused: %d %v", offset, err)
	}
	if _, err := x.db.Exec(`UPDATE telegram_state SET next_update=0; CREATE TRIGGER reject_telegram_offset BEFORE UPDATE ON telegram_state BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := x.pollTelegramUpdates(context.Background()); err == nil {
		t.Fatal("failed state write acknowledged")
	}
	if err := x.db.QueryRow(`SELECT next_update FROM telegram_state`).Scan(&saved); err != nil || saved != 0 || offset != 0 {
		t.Fatalf("failed write advanced offset: %d %d %v", saved, offset, err)
	}
	if _, err := x.db.Exec(`DROP TRIGGER reject_telegram_offset; CREATE TRIGGER reject_telegram_offset BEFORE UPDATE ON telegram_state WHEN NEW.next_update=42 BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := x.pollTelegramUpdates(context.Background()); err == nil {
		t.Fatal("partial batch failure was acknowledged")
	}
	if err := x.db.QueryRow(`SELECT next_update FROM telegram_state`).Scan(&saved); err != nil || saved != 41 {
		t.Fatalf("completed update was not persisted before later failure: %d %v", saved, err)
	}
}

func TestTelegramPollDoesNotAcknowledgeFailedLink(t *testing.T) {
	var code string
	var replies atomic.Int32
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			replies.Add(1)
			io.WriteString(w, `{"ok":true,"result":{}}`)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"result":[{"update_id":40,"message":{"chat":{"id":123,"type":"private"},"from":{"id":123,"is_bot":false},"text":"/start %s"}}]}`, code)
	})
	code = transportTestCode(t, x, 1)
	if _, err := x.db.Exec(`CREATE TRIGGER reject_telegram_link BEFORE INSERT ON telegram_links BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := x.pollTelegramUpdates(context.Background()); err == nil || replies.Load() != 0 {
		t.Fatalf("failed link was acknowledged: replies=%d err=%v", replies.Load(), err)
	}
	var offset, codes int
	if err := x.db.QueryRow(`SELECT next_update,(SELECT count(*) FROM telegram_tokens) FROM telegram_state`).Scan(&offset, &codes); err != nil || offset != 0 || codes != 1 {
		t.Fatalf("failed link consumed state: offset=%d codes=%d err=%v", offset, codes, err)
	}
}

func TestTelegramGetUpdatesRejectsInvalidBatches(t *testing.T) {
	tooMany := make([]telegramUpdate, 51)
	for i := range tooMany {
		tooMany[i].ID = int64(i)
	}
	large, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `[{"update_id":-1}]`, `[{"update_id":9223372036854775807}]`, `[{"update_id":2},{"update_id":1}]`, `[{"update_id":1},{"update_id":1}]`, string(large)} {
		bot := transportTestBot(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"ok":true,"result":%s}`, body) })
		if _, err := bot.getUpdates(context.Background(), 0); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
}

func TestTelegramNotificationStatusesAndPlainText(t *testing.T) {
	var texts []string
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 2 || body["chat_id"] != float64(123) {
			t.Errorf("unexpected notification shape: %v", body)
		}
		texts = append(texts, body["text"].(string))
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	transportTestLink(t, x, 1, 123, true)
	for _, status := range []string{"completed", "failed", "cancelled", "interrupted"} {
		id := transportTestRun(t, x, 1, "run", status, "schedule")
		if _, err := x.db.Exec(`INSERT INTO run_events(run_id,seq,data) VALUES(?,1,'{"password":"worker-private","email":"private@example.com","report":"raw report"}')`, id); err != nil {
			t.Fatal(err)
		}
		if processed, err := x.deliverTelegramNotification(context.Background()); err != nil || !processed {
			t.Fatalf("dispatch %s: %v %v", status, processed, err)
		}
	}
	if len(texts) != 4 || transportTestQueueCount(t, x) != 0 {
		t.Fatal("notifications were lost or not removed")
	}
	for i, text := range texts {
		if !strings.HasPrefix(text, []string{"✅ Lessons complete", "❌ Lessons failed", "⏹ Lessons stopped", "⚠️ Lessons interrupted"}[i]+"\n") || !strings.Contains(text, "Backend summary") || strings.Contains(text, "Run #") || strings.Contains(text, "Status:") || strings.Contains(text, "private") || strings.Contains(text, "raw report") {
			t.Fatalf("unsafe or incomplete notification: %s", text)
		}
	}
	if processed, err := x.deliverTelegramNotification(context.Background()); processed || err != nil {
		t.Fatalf("empty queue: %v %v", processed, err)
	}
	text := telegramNotificationText(RunRecord{ID: 1, Mode: "secret-mode", Source: "private@example.com", Status: "completed", Summary: strings.Repeat("ж", 1100)})
	if !utf8.ValidString(text) || strings.Contains(text, "secret-mode") || strings.Contains(text, "private@") || utf8.RuneCountInString(strings.Split(text, "\n")[1]) != 180 || !strings.HasSuffix(text, "…") {
		t.Fatal("notification labels or summary are not bounded")
	}
}

func TestTelegramNotificationCompactText(t *testing.T) {
	run := RunRecord{Mode: "run", Status: "completed", Summary: "  3 lessons completed\n\t60 words learned  ", CreatedAt: 100, FinishedAt: 230}
	if text := telegramNotificationText(run); text != "✅ Lessons complete\n3 lessons completed 60 words learned\n⏱ 2m10s" {
		t.Fatalf("unexpected compact message: %q", text)
	}
	for _, times := range [][2]int64{{0, 0}, {0, 100}, {100, 0}, {100, 99}} {
		run.CreatedAt, run.FinishedAt, run.Summary = times[0], times[1], ""
		if text := telegramNotificationText(run); text != "✅ Lessons complete" {
			t.Fatalf("missing timestamps invented duration or empty lines: %q", text)
		}
	}
	run.CreatedAt, run.FinishedAt = 100, 100
	if text := telegramNotificationText(run); text != "✅ Lessons complete\n⏱ 0s" {
		t.Fatalf("zero duration lost: %q", text)
	}
}

func TestTelegramNotificationOnlyLessons(t *testing.T) {
	var sent atomic.Int32
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	transportTestLink(t, x, 1, 123, true)
	for _, mode := range []string{"classes", "scrape", "scrape_all", "unknown"} {
		for _, status := range []string{"completed", "failed", "cancelled", "interrupted"} {
			id := transportTestRun(t, x, 1, mode, status, "manual")
			if transportTestQueueCount(t, x) != 0 {
				t.Fatalf("%s/%s queued a notification", mode, status)
			}
			// Old backups can still contain non-lesson notifications, even before their retry is due.
			if _, err := x.db.Exec(`INSERT INTO telegram_outbox(run_id,chat_id,next_attempt) VALUES(?,123,?)`, id, time.Now().Add(time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
			if processed, err := x.deliverTelegramNotification(context.Background()); err != nil || !processed || sent.Load() != 0 || transportTestQueueCount(t, x) != 0 {
				t.Fatalf("non-lesson notification was sent or retained: %v %v %d", processed, err, sent.Load())
			}
		}
	}
}

func TestTelegramNotificationRejectsStaleAndOtherOwners(t *testing.T) {
	for _, change := range []string{"muted", "disabled", "unlinked", "rebound", "wrong-chat"} {
		t.Run(change, func(t *testing.T) {
			var sent atomic.Int32
			x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				sent.Add(1)
				io.WriteString(w, `{"ok":true,"result":{}}`)
			})
			otherID := authTestUser(t, x, "other@example.com", false)
			transportTestLink(t, x, 1, 123, true)
			id := transportTestRun(t, x, 1, "run", "completed", "manual")
			var err error
			switch change {
			case "muted":
				_, err = x.db.Exec(`UPDATE telegram_links SET notifications=0 WHERE user_id=1`)
			case "disabled":
				_, err = x.db.Exec(`UPDATE users SET disabled=1 WHERE id=1`)
			case "unlinked", "rebound":
				err = x.unlinkTelegramChat(context.Background(), 123)
				if change == "rebound" {
					transportTestLink(t, x, otherID, 123, true)
				}
			case "wrong-chat":
				_, err = x.db.Exec(`UPDATE telegram_outbox SET chat_id=456 WHERE run_id=?`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Also reject stale rows restored from an old backup, not just trigger-cleaned rows.
			if _, err := x.db.Exec(`INSERT OR IGNORE INTO telegram_outbox(run_id,chat_id,next_attempt) VALUES(?,123,?)`, id, time.Now().Add(time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
			if _, err := x.deliverTelegramNotification(context.Background()); err != nil || sent.Load() != 0 || transportTestQueueCount(t, x) != 0 {
				t.Fatalf("stale notification sent: sends=%d err=%v", sent.Load(), err)
			}
		})
	}
}

func TestTelegramNotificationRetriesAndTerminalFailures(t *testing.T) {
	for _, status := range []int{400, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"ok":false,"error_code":%d,"description":"%s","parameters":{"retry_after":120}}`, status, transportTestToken)
			})
			transportTestLink(t, x, 1, 123, true)
			id := transportTestRun(t, x, 1, "run", "failed", "manual")
			before := time.Now().Unix()
			processed, err := x.deliverTelegramNotification(context.Background())
			var apiErr *telegramAPIError
			if !processed || !errors.As(err, &apiErr) || apiErr.status != status || strings.Contains(err.Error(), transportTestToken) {
				t.Fatalf("failure was not sanitized: %v %v", processed, err)
			}
			if status == 400 || status == 403 {
				var enabled bool
				if err := x.db.QueryRow(`SELECT notifications FROM telegram_links`).Scan(&enabled); err != nil || enabled || transportTestQueueCount(t, x) != 0 {
					t.Fatalf("terminal failure not removed/disabled: %v %v", enabled, err)
				}
				return
			}
			var attempts int
			var next int64
			if err := x.db.QueryRow(`SELECT attempts,next_attempt FROM telegram_outbox WHERE run_id=?`, id).Scan(&attempts, &next); err != nil || attempts != 1 || next < before+30 || status == 429 && next < before+120 {
				t.Fatalf("backoff not durable: attempts=%d next=%d err=%v", attempts, next, err)
			}
			if processed, err := x.deliverTelegramNotification(context.Background()); processed || err != nil {
				t.Fatalf("retried before due: %v %v", processed, err)
			}
			if _, err := x.db.Exec(`UPDATE telegram_outbox SET attempts=4,next_attempt=0`); err != nil {
				t.Fatal(err)
			}
			if processed, err := x.deliverTelegramNotification(context.Background()); !processed || err == nil || transportTestQueueCount(t, x) != 0 {
				t.Fatalf("retry limit was not enforced: %v %v", processed, err)
			}
		})
	}
}

func TestTelegramNotificationFailedDeletionKeepsQueue(t *testing.T) {
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true,"result":{}}`) })
	transportTestLink(t, x, 1, 123, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	if _, err := x.db.Exec(`CREATE TRIGGER reject_telegram_delete BEFORE DELETE ON telegram_outbox BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	if processed, err := x.deliverTelegramNotification(context.Background()); !processed || err == nil || transportTestQueueCount(t, x) != 1 {
		t.Fatalf("failed deletion lost durable queue item: %v %v", processed, err)
	}
}

func TestTelegramLateFailureDoesNotMuteNewBinding(t *testing.T) {
	var x *app
	x = transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := x.db.Exec(`DELETE FROM telegram_links WHERE chat_id=123;
			INSERT INTO telegram_links(user_id,chat_id,username,first_name,notifications,linked_at) VALUES(1,123,'learner','Learner',1,1);
			INSERT INTO runs(user_id,mode,class_id,source,status,created_at) VALUES(1,'run','','manual','running',0);
			UPDATE runs SET status='completed' WHERE status='running'`); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"ok":false,"error_code":403}`)
	})
	transportTestLink(t, x, 1, 123, true)
	if _, err := x.db.Exec(`UPDATE telegram_links SET linked_at=1`); err != nil {
		t.Fatal(err)
	}
	transportTestRun(t, x, 1, "run", "completed", "manual")
	if processed, err := x.deliverTelegramNotification(context.Background()); !processed || err == nil {
		t.Fatalf("late failure: %v %v", processed, err)
	}
	var enabled bool
	if err := x.db.QueryRow(`SELECT notifications FROM telegram_links`).Scan(&enabled); err != nil || !enabled || transportTestQueueCount(t, x) != 1 {
		t.Fatalf("old request muted a new binding: enabled=%v err=%v", enabled, err)
	}
}

func TestTelegramNotificationIsolatesActiveUsers(t *testing.T) {
	var chats []int64
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		chats = append(chats, body.ChatID)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	otherID := authTestUser(t, x, "other@example.com", false)
	transportTestLink(t, x, 1, 123, true)
	transportTestLink(t, x, otherID, 456, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	transportTestRun(t, x, otherID, "run", "failed", "schedule")
	for range 2 {
		if processed, err := x.deliverTelegramNotification(context.Background()); !processed || err != nil {
			t.Fatalf("dispatch: %v %v", processed, err)
		}
	}
	if !reflect.DeepEqual(chats, []int64{123, 456}) {
		t.Fatalf("notification crossed account boundaries: %v", chats)
	}
}

func TestTelegramNotificationCancellationReleasesDBAndKeepsQueue(t *testing.T) {
	started := make(chan struct{})
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	})
	transportTestLink(t, x, 1, 123, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := x.deliverTelegramNotification(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("send did not start")
	}
	dbCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var one int
	if err := x.db.QueryRowContext(dbCtx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("HTTP retained the DB connection: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send did not cancel")
	}
	var attempts int
	if err := x.db.QueryRow(`SELECT attempts FROM telegram_outbox`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("cancellation discarded or consumed queue item: %d %v", attempts, err)
	}
}

func TestTelegramLoopPollCancellationAndIndependentNotifier(t *testing.T) {
	polling, sending := make(chan struct{}), make(chan struct{})
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"Lingos_TestBot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			io.Copy(io.Discard, r.Body)
			close(polling)
			<-r.Context().Done()
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			io.Copy(io.Discard, r.Body)
			close(sending)
			<-r.Context().Done()
		}
	})
	transportTestLink(t, x, 1, 123, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); x.telegramLoop(ctx) }()
	for _, started := range []chan struct{}{polling, sending} {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("polling blocked the notification dispatcher")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Telegram loop did not wait for/cancel nested notifier")
	}
	var offset int64
	if err := x.db.QueryRow(`SELECT next_update FROM telegram_state`).Scan(&offset); err != nil || offset != 0 || transportTestQueueCount(t, x) != 1 {
		t.Fatalf("cancellation lost durable state: offset=%d err=%v", offset, err)
	}
}

func TestTelegramLoopUnavailableCancelsBackoff(t *testing.T) {
	requested := make(chan struct{})
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") {
			t.Error("notification sent before bot identity verification")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		close(requested)
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, transportTestToken)
	})
	transportTestLink(t, x, 1, 123, true)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); x.telegramLoop(ctx) }()
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("getMe did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unavailable Telegram blocked shutdown")
	}
}
