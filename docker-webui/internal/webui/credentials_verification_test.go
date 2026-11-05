package webui

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type credentialTestStatus struct {
	Saved      bool   `json:"saved"`
	Email      string `json:"email"`
	VerifiedAt int64  `json:"verified_at"`
}

func credentialStatus(t *testing.T, x *app, token string) credentialTestStatus {
	t.Helper()
	w := authTestRequest(x.credentialsAPI, "GET", "/api/credentials", "", token, "")
	var status credentialTestStatus
	var fields map[string]json.RawMessage
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil || json.Unmarshal(w.Body.Bytes(), &fields) != nil {
		t.Fatalf("credential status: %d %s", w.Code, w.Body.String())
	}
	if len(fields) != 3 || fields["saved"] == nil || fields["email"] == nil || fields["verified_at"] == nil {
		t.Fatalf("unexpected credential metadata (no passwords or fingerprints allowed): %s", w.Body.String())
	}
	return status
}

func putTestCredentials(t *testing.T, x *app, token, csrf, email, password string) {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	w := authTestRequest(x.credentialsAPI, "PUT", "/api/credentials", body, token, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("save credentials: %d %s", w.Code, w.Body.String())
	}
	var status credentialTestStatus
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.Saved || status.VerifiedAt != 0 {
		t.Fatalf("save did not reset verification: %s", w.Body.String())
	}
}

const credentialSuccess = `printf '%s\n' '{"type":"result","success":true,"classes":[]}'`

func startCredentialWorker(t *testing.T, x *app, userID int64, mode, script string) int64 {
	t.Helper()
	x.worker = filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(x.worker, []byte("#!/bin/sh\ncat >/dev/null\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := x.startRun(userID, mode, "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func credentialRunStatus(t *testing.T, x *app, id int64) string {
	t.Helper()
	x.workers.Wait()
	var status string
	if err := x.db.QueryRow(`SELECT status FROM runs WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestCredentialsVerificationSurvivesReloadLogoutAndRestart(t *testing.T) {
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	other := authTestUser(t, x, "other@example.com", false)
	otherToken, otherCSRF := authTestSession(t, x, other)
	for _, session := range []struct{ token, csrf string }{{token, csrf}, {otherToken, otherCSRF}} {
		putTestCredentials(t, x, session.token, session.csrf, "learner@example.com", "worker-secret")
		if status := credentialStatus(t, x, session.token); !status.Saved || status.VerifiedAt != 0 {
			t.Fatalf("new credentials already verified: %+v", status)
		}
	}
	id := startCredentialWorker(t, x, 1, "classes", "test \"$3\" = --list-classes || exit 1\n"+credentialSuccess)
	if status := credentialRunStatus(t, x, id); status != "completed" {
		t.Fatalf("class sync: %s", status)
	}
	verified := credentialStatus(t, x, token)
	if !verified.Saved || verified.Email != "learner@example.com" || verified.VerifiedAt <= 0 {
		t.Fatalf("successful check was not persisted: %+v", verified)
	}
	if status := credentialStatus(t, x, otherToken); status.VerifiedAt != 0 {
		t.Fatal("check verified another user's identical credentials")
	}
	if w := authTestRequest(x.logout, "POST", "/logout", "", token, csrf); w.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d %s", w.Code, w.Body.String())
	}
	cookie := loginCookie(t, x)
	if status := credentialStatus(t, x, cookie.Value); status != verified {
		t.Fatalf("logout/login changed verification: %+v", status)
	}
	var seq int
	var name, dbPath string
	if err := x.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &dbPath); err != nil {
		t.Fatal(err)
	}
	if err := x.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reloaded := &app{db: db, aead: x.aead, admin: x.admin, password: x.password}
	for _, init := range []func() error{func() error { return initDB(db) }, reloaded.bootstrapAdmin, func() error { return initRunnerDB(db) }} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	if status := credentialStatus(t, reloaded, cookie.Value); status != verified {
		t.Fatalf("new app on the same database lost verification: %+v", status)
	}
	if status := credentialStatus(t, reloaded, otherToken); status.VerifiedAt != 0 {
		t.Fatal("restart verified another user's credentials")
	}
}

func TestCredentialsVerificationRevisionRacesAndReset(t *testing.T) {
	for _, change := range []string{"same save", "password", "email", "delete", "delete and resave"} {
		t.Run(change, func(t *testing.T) {
			x := integrationApp(t)
			token, csrf := authTestSession(t, x, 1)
			putTestCredentials(t, x, token, csrf, "learner@example.com", "worker-secret")
			id := startCredentialWorker(t, x, 1, "classes", credentialSuccess)
			if credentialRunStatus(t, x, id) != "completed" || credentialStatus(t, x, token).VerifiedAt <= 0 {
				t.Fatal("initial verification failed")
			}
			release := filepath.Join(t.TempDir(), "release")
			id = startCredentialWorker(t, x, 1, "classes", "while [ ! -f '"+release+"' ]; do sleep 0.01; done\n"+credentialSuccess)
			email, password := "learner@example.com", "worker-secret"
			switch change {
			case "password":
				password = "replacement-secret"
			case "email":
				email = "replacement@example.com"
			case "delete", "delete and resave":
				if w := authTestRequest(x.credentialsAPI, "DELETE", "/api/credentials", "", token, csrf); w.Code != http.StatusOK {
					t.Fatalf("delete: %d %s", w.Code, w.Body.String())
				}
			}
			if change != "delete" {
				putTestCredentials(t, x, token, csrf, email, password)
			}
			if status := credentialStatus(t, x, token); status.VerifiedAt != 0 {
				t.Fatalf("credential change did not reset verification: %+v", status)
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if status := credentialRunStatus(t, x, id); status != "completed" {
				t.Fatalf("old class sync: %s", status)
			}
			status := credentialStatus(t, x, token)
			if status.VerifiedAt != 0 || status.Saved != (change != "delete") {
				t.Fatalf("old check verified new credentials or resurrected deletion: %+v", status)
			}
			if change != "delete" {
				id = startCredentialWorker(t, x, 1, "classes", credentialSuccess)
				if credentialRunStatus(t, x, id) != "completed" || credentialStatus(t, x, token).VerifiedAt <= 0 {
					t.Fatal("new revision could not be verified")
				}
			}
		})
	}
}

func TestCredentialsVerificationFailurePreservesPreviousSuccess(t *testing.T) {
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	putTestCredentials(t, x, token, csrf, "learner@example.com", "worker-secret")
	id := startCredentialWorker(t, x, 1, "run", `printf '%s\n' '{"type":"result","success":true}'`)
	if credentialRunStatus(t, x, id) != "completed" || credentialStatus(t, x, token).VerifiedAt != 0 {
		t.Fatal("a non-class run verified credentials")
	}
	id = startCredentialWorker(t, x, 1, "classes", `printf '%s\n' '{"type":"result","success":false}'`)
	if credentialRunStatus(t, x, id) != "failed" || credentialStatus(t, x, token).VerifiedAt != 0 {
		t.Fatal("an unsuccessful initial check verified credentials")
	}
	id = startCredentialWorker(t, x, 1, "classes", credentialSuccess)
	if credentialRunStatus(t, x, id) != "completed" {
		t.Fatal("initial successful class sync failed")
	}
	verified := credentialStatus(t, x, token)
	if verified.VerifiedAt <= 0 {
		t.Fatal("initial verification was not saved")
	}
	for name, script := range map[string]string{
		"failed login":     `printf '%s\n' '{"type":"result","success":false}'`,
		"network failure":  "exit 1",
		"invalid output":   "printf 'invalid\\n'",
		"invalid classes":  `printf '%s\n' '{"type":"result","success":true,"classes":null}'`,
		"duplicate result": credentialSuccess + "\n" + credentialSuccess,
		"late exit error":  credentialSuccess + "\nexit 1",
	} {
		t.Run(name, func(t *testing.T) {
			id := startCredentialWorker(t, x, 1, "classes", script)
			if credentialRunStatus(t, x, id) != "failed" {
				t.Fatal("invalid class sync succeeded")
			}
			if status := credentialStatus(t, x, token); status != verified {
				t.Fatalf("failed recheck erased previous success: %+v", status)
			}
		})
	}
	if w := authTestRequest(x.credentialsAPI, "PUT", "/api/credentials", `{"email":"invalid","password":"secret"}`, token, csrf); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid credentials accepted: %d %s", w.Code, w.Body.String())
	}
	if status := credentialStatus(t, x, token); status != verified {
		t.Fatal("rejected save erased verification")
	}
	if _, err := x.db.Exec(`CREATE TRIGGER reject_verification BEFORE UPDATE OF verified_at ON user_credentials BEGIN SELECT RAISE(ABORT,'storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	id = startCredentialWorker(t, x, 1, "classes", credentialSuccess)
	if credentialRunStatus(t, x, id) != "failed" || credentialStatus(t, x, token) != verified {
		t.Fatal("failed verification write was hidden or erased previous success")
	}
}

func TestCredentialsVerificationWaitsForCleanExit(t *testing.T) {
	x := integrationApp(t)
	token, csrf := authTestSession(t, x, 1)
	putTestCredentials(t, x, token, csrf, "learner@example.com", "worker-secret")
	id := startCredentialWorker(t, x, 1, "classes", credentialSuccess+"\nexec sleep 60")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var events int
		if err := x.db.QueryRow(`SELECT count(*) FROM run_events WHERE run_id=?`, id).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not report its result")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if credentialStatus(t, x, token).VerifiedAt != 0 {
		t.Fatal("result event verified credentials before the worker finished")
	}
	if err := x.cancelUserRuns(1); err != nil {
		t.Fatal(err)
	}
	if credentialRunStatus(t, x, id) != "failed" || credentialStatus(t, x, token).VerifiedAt != 0 {
		t.Fatal("cancelled class sync verified credentials")
	}
}

func TestCredentialsVerificationMigratesExistingV2WithoutInferringOldSuccess(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	putTestCredentials(t, x, token, csrf, "learner@example.com", "worker-secret")
	_, before, err := x.loadEncryptedCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the v2 table with a successful run that has no credential-revision proof.
	if _, err := x.db.Exec(`DROP TRIGGER reset_credential_verification;
		ALTER TABLE user_credentials DROP COLUMN verified_at;
		INSERT INTO runs(user_id,mode,class_id,source,status,created_at,finished_at) VALUES(1,'classes','','manual','completed',1,2)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := initDB(x.db); err != nil {
			t.Fatal(err)
		}
		if status := credentialStatus(t, x, token); !status.Saved || status.VerifiedAt != 0 {
			t.Fatalf("migration inferred verification from an old run or lost credentials: %+v", status)
		}
	}
	_, after, err := x.loadEncryptedCredentials(1)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("migration changed encrypted credentials")
	}
	if email, password, err := x.loadCredentials(1); err != nil || email != "learner@example.com" || password != "worker-secret" {
		t.Fatal("migration lost the existing credentials")
	}
	if _, err := x.db.Exec(`UPDATE user_credentials SET verified_at=123 WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := initDB(x.db); err != nil {
		t.Fatal(err)
	}
	if status := credentialStatus(t, x, token); status.VerifiedAt != 123 {
		t.Fatal("repeated migration reset persisted verification")
	}
}

func TestCredentialsVerificationResetsForOlderCredentialWriters(t *testing.T) {
	x := newAuthTestApp(t)
	token, csrf := authTestSession(t, x, 1)
	putTestCredentials(t, x, token, csrf, "learner@example.com", "worker-secret")
	encrypted, err := x.encryptCredentials(1, "replacement-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		query string
		value any
	}{
		{`UPDATE user_credentials SET email=? WHERE user_id=1`, "replacement@example.com"},
		{`UPDATE user_credentials SET password=? WHERE user_id=1`, encrypted},
	} {
		if _, err := x.db.Exec(`UPDATE user_credentials SET verified_at=123 WHERE user_id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := x.db.Exec(change.query, change.value); err != nil {
			t.Fatal(err)
		}
		if status := credentialStatus(t, x, token); status.VerifiedAt != 0 {
			t.Fatal("an older credential writer left a stale verification")
		}
	}
}
