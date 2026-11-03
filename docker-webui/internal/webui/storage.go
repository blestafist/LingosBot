package webui

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

func initDB(db *sql.DB) error {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS credentials (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, password BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, csrf TEXT NOT NULL, expires INTEGER NOT NULL);
DELETE FROM sessions;
CREATE TABLE IF NOT EXISTS schedule (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL, time TEXT NOT NULL, lessons INTEGER NOT NULL, errors INTEGER NOT NULL, last_date TEXT NOT NULL DEFAULT '');
INSERT OR IGNORE INTO schedule(id,enabled,time,lessons,errors) VALUES(1,0,'09:00',1,0);
CREATE TABLE IF NOT EXISTS users (
 id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash BLOB NOT NULL,
 admin INTEGER NOT NULL DEFAULT 0 CHECK(admin IN (0,1)), disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)), created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS user_sessions (
 token TEXT PRIMARY KEY, csrf TEXT NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS user_sessions_user ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS user_sessions_expiry ON user_sessions(expires);
CREATE TABLE IF NOT EXISTS user_credentials (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, email TEXT NOT NULL, password BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_meta (key TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS auth_limits (key TEXT PRIMARY KEY, attempts INTEGER NOT NULL, expires INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS auth_limits_expiry ON auth_limits(expires);
`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (x *app) bootstrapAdmin() error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		email := strings.ToLower(strings.TrimSpace(x.admin))
		if email == "" || len(email) > 254 {
			return fmt.Errorf("ADMIN_USER required (at most 254 bytes)")
		}
		hash, err := hashPassword(x.password)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO users(id,email,password_hash,admin,created_at) VALUES(1,?,?,1,?)`, email, hash, time.Now().Unix()); err != nil {
			return err
		}
	}
	var migrated bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM auth_meta WHERE key='legacy_credentials_v1')`).Scan(&migrated); err != nil {
		return err
	}
	if !migrated {
		var email string
		var encrypted []byte
		err = tx.QueryRow(`SELECT username,password FROM credentials WHERE id=1`).Scan(&email, &encrypted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			password, err := x.decryptCredentials(1, encrypted, false)
			if err != nil {
				return fmt.Errorf("legacy credential migration: %w", err)
			}
			encrypted, err = x.encryptCredentials(1, password)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO user_credentials(user_id,email,password) VALUES(1,?,?) ON CONFLICT(user_id) DO NOTHING`, email, encrypted); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO auth_meta(key) VALUES('legacy_credentials_v1')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func credentialAAD(userID int64) []byte {
	return []byte(fmt.Sprintf("lingos:user_credentials:%d", userID))
}

func (x *app) encryptCredentials(userID int64, password string) ([]byte, error) {
	if x.aead == nil {
		return nil, fmt.Errorf("encryption unavailable")
	}
	nonce := make([]byte, x.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return x.aead.Seal(nonce, nonce, []byte(password), credentialAAD(userID)), nil
}

func (x *app) decryptCredentials(userID int64, encrypted []byte, bound bool) (string, error) {
	if x.aead == nil || len(encrypted) < x.aead.NonceSize()+x.aead.Overhead() {
		return "", fmt.Errorf("invalid encrypted credentials")
	}
	n := x.aead.NonceSize()
	var aad []byte
	if bound {
		aad = credentialAAD(userID)
	}
	password, err := x.aead.Open(nil, encrypted[:n], encrypted[n:], aad)
	return string(password), err
}

func (x *app) loadCredentials(userID int64) (email, password string, err error) {
	var encrypted []byte
	err = x.db.QueryRow(`SELECT c.email,c.password FROM user_credentials c JOIN users u ON u.id=c.user_id WHERE c.user_id=? AND u.disabled=0`, userID).Scan(&email, &encrypted)
	if err != nil {
		return "", "", err
	}
	password, err = x.decryptCredentials(userID, encrypted, true)
	if err != nil {
		return "", "", err
	}
	return email, password, nil
}

var errRateLimited = errors.New("rate limited")

func loginLimitKeys(r *http.Request, email string) (ipKey, emailKey string) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	ipKey = "ip:" + tokenHash(ip)
	return ipKey, "email:" + tokenHash(ipKey+"\x00"+email)
}

func (x *app) loginLimit(r *http.Request, email string) error {
	ipKey, emailKey := loginLimitKeys(r, email)
	tx, err := x.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err = tx.Exec(`DELETE FROM auth_limits WHERE key IN (SELECT key FROM auth_limits WHERE expires<=? LIMIT 100)`, now); err != nil {
		return err
	}
	for _, limit := range []struct {
		key string
		max int
	}{{ipKey, 30}, {emailKey, 10}} {
		var attempts int
		var expires int64
		err = tx.QueryRow(`SELECT attempts,expires FROM auth_limits WHERE key=?`, limit.key).Scan(&attempts, &expires)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			var count int
			if err = tx.QueryRow(`SELECT count(*) FROM auth_limits`).Scan(&count); err != nil {
				return err
			}
			if count >= 10000 {
				return errRateLimited
			}
		}
		if expires <= now {
			attempts = 0
			expires = now + 15*60
		}
		if attempts >= limit.max {
			return errRateLimited
		}
		if _, err = tx.Exec(`INSERT INTO auth_limits(key,attempts,expires) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET attempts=excluded.attempts,expires=excluded.expires`, limit.key, attempts+1, expires); err != nil {
			return err
		}
	}
	return tx.Commit()
}
