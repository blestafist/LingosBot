package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func patchTelegramRegistrationTest(t *testing.T, x *app, userID int64, body string, want int) {
	t.Helper()
	token, csrf := authTestSession(t, x, userID)
	w := authTestRequest(x.routes().ServeHTTP, http.MethodPatch, "/api/telegram", body, token, csrf)
	if w.Code != want {
		t.Fatalf("PATCH %s: %d %s (want %d)", body, w.Code, w.Body.String(), want)
	}
}

func telegramRegistrationTestStatus(t *testing.T, x *app, userID int64) telegramStatus {
	t.Helper()
	token, _ := authTestSession(t, x, userID)
	w := authTestRequest(x.routes().ServeHTTP, http.MethodGet, "/api/telegram", "", token, "")
	var status telegramStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != 200 || err != nil || !strings.Contains(w.Body.String(), `"registration_notifications":`) {
		t.Fatalf("GET Telegram: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	return status
}

func registerTelegramTestApplicant(t *testing.T, x *app, email string, want int) {
	t.Helper()
	w := registrationTestRequest(x, email, authTestPassword)
	if w.Code != want {
		t.Fatalf("register %s: %d %s (want %d)", email, w.Code, w.Body.String(), want)
	}
}

func assertTelegramRegistrationQueue(t *testing.T, x *app, want int) {
	t.Helper()
	var count int
	if err := x.db.QueryRow(`SELECT count(*) FROM telegram_registration_outbox`).Scan(&count); err != nil || count != want {
		t.Fatalf("registration outbox: %d, want %d (%v)", count, want, err)
	}
}

func TestTelegramRegistrationPreferenceMigrationAndAPI(t *testing.T) {
	x := newAuthTestApp(t)
	if _, err := x.db.Exec(`CREATE TABLE telegram_links (
		user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		chat_id INTEGER NOT NULL UNIQUE CHECK(chat_id>0), username TEXT NOT NULL, first_name TEXT NOT NULL,
		notifications INTEGER NOT NULL DEFAULT 0 CHECK(notifications IN (0,1)), linked_at INTEGER NOT NULL);
		INSERT INTO telegram_links VALUES(1,123,'admin','Admin',1,1234)`); err != nil {
		t.Fatal(err)
	}
	if err := initTelegramDB(x.db); err != nil {
		t.Fatal(err)
	}
	if s := telegramRegistrationTestStatus(t, x, 1); !s.Linked || !s.Notifications || s.RegistrationNotifications {
		t.Fatalf("migration changed prior consent: %+v", s)
	}
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":true,"notifications":false}`, 200)
	if err := initTelegramDB(x.db); err != nil {
		t.Fatal(err)
	}
	patchTelegramRegistrationTest(t, x, 1, `{"notifications":true}`, 200)
	if s := telegramRegistrationTestStatus(t, x, 1); !s.Notifications || !s.RegistrationNotifications {
		t.Fatalf("run preference overwrote registration preference: %+v", s)
	}
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":false}`, 200)
	if s := telegramRegistrationTestStatus(t, x, 1); !s.Notifications || s.RegistrationNotifications {
		t.Fatalf("registration preference overwrote run preference: %+v", s)
	}
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":"true"}`, 400)
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":null}`, 400)
	patchTelegramRegistrationTest(t, x, authTestUser(t, x, "unlinked@example.com", true), `{"registration_notifications":true}`, 409)
	nonAdmin := authTestUser(t, x, "learner@example.com", false)
	transportTestLink(t, x, nonAdmin, 456, false)
	patchTelegramRegistrationTest(t, x, nonAdmin, `{"registration_notifications":true,"notifications":true}`, 403)
	if s := telegramRegistrationTestStatus(t, x, nonAdmin); s.Notifications || s.RegistrationNotifications {
		t.Fatalf("forbidden combined patch partially applied: %+v", s)
	}
}

func TestTelegramRegistrationNewPendingRequestsOnly(t *testing.T) {
	var chats []int64
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.HasSuffix(r.URL.Path, "/sendMessage") || body.Text != "A new registration request is pending. Review it in LingosBot under Users." {
			t.Errorf("invalid registration alert: %+v (%v)", body, err)
		}
		chats = append(chats, body.ChatID)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	setRegistrationTestMode(t, x, registrationApproval)
	registerTelegramTestApplicant(t, x, "historical@example.com", 201)
	transportTestLink(t, x, 1, 123, false)
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":true}`, 200)
	second := authTestUser(t, x, "other-admin@example.com", true)
	transportTestLink(t, x, second, 456, true)
	patchTelegramRegistrationTest(t, x, second, `{"registration_notifications":true}`, 200)
	transportTestLink(t, x, authTestUser(t, x, "lesson-only@example.com", true), 789, true)
	authTestUser(t, x, "unlinked@example.com", true)
	assertTelegramRegistrationQueue(t, x, 0)

	registerTelegramTestApplicant(t, x, "applicant@example.com", 201)
	registerTelegramTestApplicant(t, x, "APPLICANT@EXAMPLE.COM", 201)
	setRegistrationTestMode(t, x, registrationOpen)
	registerTelegramTestApplicant(t, x, "open@example.com", 201)
	setRegistrationTestMode(t, x, registrationAdminOnly)
	registerTelegramTestApplicant(t, x, "closed@example.com", 403)
	assertTelegramRegistrationQueue(t, x, 2)
	if len(chats) != 0 {
		t.Fatal("register sent Telegram inline")
	}
	for range 2 {
		if processed, err := x.deliverTelegramRegistrationNotification(t.Context()); err != nil || !processed {
			t.Fatalf("deliver: %v %v", processed, err)
		}
	}
	if !reflect.DeepEqual(chats, []int64{123, 456}) {
		t.Fatalf("unexpected recipients: %v", chats)
	}
	assertTelegramRegistrationQueue(t, x, 0)
}

func TestTelegramRegistrationQueueAtomicityAndNoBot(t *testing.T) {
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected Telegram request") })
	setRegistrationTestMode(t, x, registrationApproval)
	transportTestLink(t, x, 1, 123, true)
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":true}`, 200)
	if _, err := x.db.Exec(`CREATE TRIGGER reject_registration_alert BEFORE INSERT ON telegram_registration_outbox BEGIN SELECT RAISE(ABORT,'storage failed'); END`); err != nil {
		t.Fatal(err)
	}
	var originalCounter int
	if err := x.db.QueryRow(`SELECT last_id FROM user_id_counter`).Scan(&originalCounter); err != nil {
		t.Fatal(err)
	}
	registerTelegramTestApplicant(t, x, "retry@example.com", 500)
	var count, counter int
	if err := x.db.QueryRow(`SELECT (SELECT count(*) FROM users), last_id FROM user_id_counter`).Scan(&count, &counter); err != nil || count != 1 || counter != originalCounter {
		t.Fatalf("queue failure did not roll back signup: users=%d counter=%d err=%v", count, counter, err)
	}
	x.telegram = nil
	registerTelegramTestApplicant(t, x, "retry@example.com", 201)
	assertTelegramRegistrationQueue(t, x, 0)
}

func TestTelegramRegistrationStopsQueuedAlerts(t *testing.T) {
	for _, change := range []string{"approve", "reject", "disable", "demote", "unlink", "opt-out"} {
		t.Run(change, func(t *testing.T) {
			x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("stale Telegram alert sent") })
			if err := initLibraryDB(x.db); err != nil {
				t.Fatal(err)
			}
			setRegistrationTestMode(t, x, registrationApproval)
			adminID := authTestUser(t, x, "linked-admin@example.com", true)
			transportTestLink(t, x, adminID, 123, true)
			patchTelegramRegistrationTest(t, x, adminID, `{"registration_notifications":true}`, 200)
			registerTelegramTestApplicant(t, x, "applicant@example.com", 201)
			assertTelegramRegistrationQueue(t, x, 1)
			var applicantID int64
			if err := x.db.QueryRow(`SELECT id FROM users WHERE pending=1`).Scan(&applicantID); err != nil {
				t.Fatal(err)
			}
			adminMutation := func(method string, path, body string) {
				t.Helper()
				token, csrf := authTestSession(t, x, 1)
				w := authTestRequest(x.routes().ServeHTTP, method, "/api/admin/users/"+path, body, token, csrf)
				if w.Code != 200 {
					t.Fatalf("admin mutation: %d %s", w.Code, w.Body.String())
				}
			}
			switch change {
			case "approve":
				adminMutation(http.MethodPatch, fmt.Sprint(applicantID), `{"approve":true}`)
			case "reject":
				adminMutation(http.MethodDelete, fmt.Sprint(applicantID)+"?pending=1", "")
			case "disable":
				adminMutation(http.MethodPatch, fmt.Sprint(adminID), `{"disabled":true}`)
				adminMutation(http.MethodPatch, fmt.Sprint(adminID), `{"disabled":false}`)
			case "demote":
				adminMutation(http.MethodPatch, fmt.Sprint(adminID), `{"admin":false}`)
				adminMutation(http.MethodPatch, fmt.Sprint(adminID), `{"admin":true}`)
			case "unlink":
				if err := x.unlinkTelegramChat(t.Context(), 123); err != nil {
					t.Fatal(err)
				}
				transportTestLink(t, x, adminID, 123, true)
			case "opt-out":
				patchTelegramRegistrationTest(t, x, adminID, `{"registration_notifications":false}`, 200)
			}
			assertTelegramRegistrationQueue(t, x, 0)
			if processed, err := x.deliverTelegramRegistrationNotification(t.Context()); processed || err != nil {
				t.Fatalf("unexpected delivery: %v %v", processed, err)
			}
			if change != "approve" && change != "reject" {
				if s := telegramRegistrationTestStatus(t, x, adminID); !s.Notifications || s.RegistrationNotifications {
					t.Fatalf("registration consent survived revocation or affected lesson consent: %+v", s)
				}
				registerTelegramTestApplicant(t, x, "next@example.com", 201)
				assertTelegramRegistrationQueue(t, x, 0)
			}
		})
	}
}

func TestTelegramRegistrationRetriesAndMutesOnTerminalFailure(t *testing.T) {
	var calls atomic.Int32
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":120}}`)
			return
		}
		w.WriteHeader(403)
		io.WriteString(w, `{"ok":false,"error_code":403}`)
	})
	setRegistrationTestMode(t, x, registrationApproval)
	transportTestLink(t, x, 1, 123, true)
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":true}`, 200)
	registerTelegramTestApplicant(t, x, "retry@example.com", 201)
	before := time.Now().Unix()
	processed, err := x.deliverTelegramRegistrationNotification(t.Context())
	var apiErr *telegramAPIError
	if !processed || !errors.As(err, &apiErr) || apiErr.status != 429 {
		t.Fatalf("first delivery: %v %v", processed, err)
	}
	var attempts int
	var due int64
	if err := x.db.QueryRow(`SELECT attempts,next_attempt FROM telegram_registration_outbox`).Scan(&attempts, &due); err != nil || attempts != 1 || due < before+120 {
		t.Fatalf("retry delay not saved: attempts=%d due=%d err=%v", attempts, due, err)
	}
	if processed, err := x.deliverTelegramRegistrationNotification(t.Context()); processed || err != nil || calls.Load() != 1 {
		t.Fatalf("early retry: %v %v", processed, err)
	}
	registerTelegramTestApplicant(t, x, "another@example.com", 201)
	processed, err = x.deliverTelegramRegistrationNotification(t.Context())
	if !processed || !errors.As(err, &apiErr) || apiErr.status != 403 {
		t.Fatalf("terminal delivery: %v %v", processed, err)
	}
	assertTelegramRegistrationQueue(t, x, 0)
	if s := telegramRegistrationTestStatus(t, x, 1); !s.Notifications || s.RegistrationNotifications {
		t.Fatalf("terminal failure affected wrong preference: %+v", s)
	}
}

func TestTelegramRegistrationNotifierDispatchesBothQueues(t *testing.T) {
	texts := make(chan string, 2)
	x := transportTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Text string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		select {
		case texts <- body.Text:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	setRegistrationTestMode(t, x, registrationApproval)
	transportTestLink(t, x, 1, 123, true)
	patchTelegramRegistrationTest(t, x, 1, `{"registration_notifications":true}`, 200)
	registerTelegramTestApplicant(t, x, "applicant@example.com", 201)
	transportTestRun(t, x, 1, "run", "completed", "manual")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		x.telegramNotificationLoop(ctx)
	}()
	defer func() { cancel(); <-done }()
	for _, prefix := range []string{"✅ Lessons complete", "A new registration request is pending."} {
		select {
		case text := <-texts:
			if !strings.HasPrefix(text, prefix) {
				t.Fatalf("expected prefix %q, got %q", prefix, text)
			}
		case <-ctx.Done():
			t.Fatalf("notification loop did not deliver %q", prefix)
		}
	}
}
