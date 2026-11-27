package webui

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

func initTelegramRegistrationDB(tx *sql.Tx) error {
	var hasPreference bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('telegram_links') WHERE name='registration_notifications')`).Scan(&hasPreference)
	if err != nil {
		return err
	}
	if !hasPreference {
		if _, err := tx.Exec(`ALTER TABLE telegram_links ADD COLUMN registration_notifications INTEGER NOT NULL DEFAULT 0 CHECK(registration_notifications IN (0,1))`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS telegram_registration_outbox (
 applicant_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 admin_id INTEGER NOT NULL REFERENCES telegram_links(user_id) ON DELETE CASCADE,
 chat_id INTEGER NOT NULL CHECK(chat_id>0),
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(applicant_id,admin_id)
);
CREATE INDEX IF NOT EXISTS telegram_registration_outbox_admin ON telegram_registration_outbox(admin_id);
CREATE INDEX IF NOT EXISTS telegram_registration_outbox_due ON telegram_registration_outbox(next_attempt,applicant_id,admin_id);
CREATE TRIGGER IF NOT EXISTS telegram_registration_handled AFTER UPDATE OF pending ON users
WHEN OLD.pending=1 AND NEW.pending=0
BEGIN
 DELETE FROM telegram_registration_outbox WHERE applicant_id=NEW.id;
END;
CREATE TRIGGER IF NOT EXISTS telegram_registration_muted AFTER UPDATE OF registration_notifications ON telegram_links
WHEN OLD.registration_notifications=1 AND NEW.registration_notifications=0
BEGIN
 DELETE FROM telegram_registration_outbox WHERE admin_id=NEW.user_id;
END;
CREATE TRIGGER IF NOT EXISTS telegram_registration_admin_revoked AFTER UPDATE OF admin,disabled ON users
WHEN OLD.admin=1 AND OLD.disabled=0 AND (NEW.admin=0 OR NEW.disabled=1)
BEGIN
 UPDATE telegram_links SET registration_notifications=0 WHERE user_id=NEW.id AND registration_notifications=1;
END;`)
	return err
}

func queueTelegramRegistrationNotifications(tx *sql.Tx, applicantID int64) error {
	_, err := tx.Exec(`INSERT INTO telegram_registration_outbox(applicant_id,admin_id,chat_id)
		SELECT ?,l.user_id,l.chat_id FROM telegram_links l JOIN users u ON u.id=l.user_id
		WHERE l.registration_notifications=1 AND u.admin=1 AND u.disabled=0`, applicantID)
	return err
}

func (x *app) deliverTelegramRegistrationNotification(ctx context.Context) (bool, error) {
	var applicantID, adminID, chatID int64
	var attempts int
	var active bool
	err := x.db.QueryRowContext(ctx, `SELECT q.applicant_id,q.admin_id,q.chat_id,q.attempts,
		applicant.pending=1 AND recipient.admin=1 AND recipient.disabled=0 AND l.registration_notifications=1 AND l.chat_id=q.chat_id
		FROM telegram_registration_outbox q JOIN users applicant ON applicant.id=q.applicant_id
		JOIN users recipient ON recipient.id=q.admin_id JOIN telegram_links l ON l.user_id=q.admin_id
		WHERE q.next_attempt<=? ORDER BY q.next_attempt,q.applicant_id,q.admin_id LIMIT 1`, time.Now().Unix()).
		Scan(&applicantID, &adminID, &chatID, &attempts, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	const deleteAlert = `DELETE FROM telegram_registration_outbox WHERE applicant_id=? AND admin_id=? AND chat_id=?`
	if !active {
		_, err := x.db.ExecContext(ctx, deleteAlert, applicantID, adminID, chatID)
		return true, err
	}
	// Release SQLite before HTTP; a later account change can clear queued retries, but cannot recall this send.
	sendErr := x.telegram.sendMessage(ctx, chatID, "A new registration request is pending. Review it in LingosBot under Users.")
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	var apiErr *telegramAPIError
	switch {
	case sendErr == nil:
		// shortcut: a crash before deleting the alert can duplicate a send; upgrade if Telegram supports idempotency.
		_, err = x.db.ExecContext(ctx, deleteAlert, applicantID, adminID, chatID)
	case errors.As(sendErr, &apiErr) && (apiErr.status == http.StatusBadRequest || apiErr.status == http.StatusForbidden):
		// A late failure must not mute a newly linked chat or a preference that was toggled off and back on.
		_, err = x.db.ExecContext(ctx, `UPDATE telegram_links SET registration_notifications=0 WHERE user_id=? AND chat_id=?
			AND EXISTS(SELECT 1 FROM telegram_registration_outbox WHERE applicant_id=? AND admin_id=? AND chat_id=?)`,
			adminID, chatID, applicantID, adminID, chatID)
	case attempts >= 4:
		_, err = x.db.ExecContext(ctx, deleteAlert, applicantID, adminID, chatID)
	default:
		delay := telegramNotificationRetryDelay(attempts, apiErr)
		_, err = x.db.ExecContext(ctx, `UPDATE telegram_registration_outbox SET attempts=attempts+1,next_attempt=? WHERE applicant_id=? AND admin_id=? AND chat_id=?`,
			time.Now().Add(delay).Unix(), applicantID, adminID, chatID)
	}
	if err != nil {
		return true, err
	}
	return true, sendErr
}
