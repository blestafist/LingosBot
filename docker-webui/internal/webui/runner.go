package webui

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	classID        = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	errWorkerBusy  = errors.New("Another task is running. Please wait for it to finish.")
	errRunStorage  = errors.New("Could not save the run. Ask the administrator to check server storage.")
	errCredentials = errors.New("Save your Lingos email and password in Account first. Check that the encryption key has not changed.")
)

type RunRecord struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	Mode         string `json:"mode"`
	ClassID      string `json:"class_id"`
	Source       string `json:"source"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	CreatedAt    int64  `json:"created_at"`
	FinishedAt   int64  `json:"finished_at"`
	ScheduledFor *int64 `json:"scheduled_for"`
}

func initRunnerDB(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS runs (
		id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id),
		mode TEXT NOT NULL,class_id TEXT NOT NULL,source TEXT NOT NULL,status TEXT NOT NULL,
		summary TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,finished_at INTEGER NOT NULL DEFAULT 0,
		scheduled_for INTEGER, UNIQUE(user_id,scheduled_for));
		CREATE INDEX IF NOT EXISTS runs_user ON runs(user_id,id);
		CREATE UNIQUE INDEX IF NOT EXISTS one_running_worker ON runs(status) WHERE status='running';
		CREATE TABLE IF NOT EXISTS run_events (run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(run_id,seq));
		CREATE TABLE IF NOT EXISTS class_cache (user_id INTEGER PRIMARY KEY REFERENCES users(id),data TEXT NOT NULL);`)
	return err
}

func workerArgs(mode, id string) ([]string, error) {
	if mode != "scrape" && id != "" {
		return nil, errors.New("class_id is only allowed for vocabulary collection.")
	}
	args := []string{"--realtime", "--config-json-stdin"}
	switch mode {
	case "classes":
		return append(args, "--list-classes"), nil
	case "scrape":
		if !classID.MatchString(id) {
			return nil, errors.New("Enter a positive numeric class ID.")
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return nil, errors.New("The class ID is out of range.")
		}
		return append(args, "--scrape-class", id), nil
	case "run":
		return args, nil
	default:
		return nil, errors.New("Choose lessons, class sync or vocabulary collection.")
	}
}

func (x *app) startRun(userID int64, mode, id, source string) (int64, error) {
	return x.launchRun(userID, mode, id, source, nil, nil)
}

func (x *app) startScheduledRun(userID int64, due int64) (int64, error) {
	var id int64
	err := x.db.QueryRow(`SELECT id FROM runs WHERE user_id=? AND scheduled_for=?`, userID, due).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return x.launchRun(userID, "run", "", "schedule", &due, nil)
}

func (x *app) launchRun(userID int64, mode, id, source string, due *int64, lessonCount *int) (int64, error) {
	args, err := workerArgs(mode, id)
	if err != nil {
		return 0, err
	}
	if lessonCount != nil {
		if mode != "run" {
			return 0, errors.New("lesson_count is only allowed for lesson runs.")
		}
		if *lessonCount < 1 || *lessonCount > 10000 {
			return 0, errors.New("lesson_count must be between 1 and 10000.")
		}
	}
	if !x.workerMu.TryLock() {
		return 0, errWorkerBusy
	}
	started := false
	defer func() {
		if !started {
			x.workerMu.Unlock()
		}
	}()
	var disabled bool
	if err := x.db.QueryRow(`SELECT disabled FROM users WHERE id=?`, userID).Scan(&disabled); err != nil || disabled {
		return 0, errors.New("This account is not available.")
	}
	s, err := x.loadSettings(userID)
	if err != nil {
		return 0, errors.New("Could not load lesson settings.")
	}
	if lessonCount != nil {
		s.LessonCount = *lessonCount
	}
	limit, err := workerExecutionLimit()
	if err != nil {
		return 0, err
	}
	s.MaxRunSeconds = min(s.MaxRunSeconds, limit)
	email, password, err := x.loadCredentials(userID)
	if err != nil {
		return 0, errCredentials
	}
	configJSON, err := json.Marshal(s)
	if err != nil {
		return 0, err
	}
	var config map[string]any
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return 0, err
	}
	config["credentials"] = map[string]string{"email": email, "password": password}
	config["baseUrl"] = env("WORKER_BASE_URL", "https://lingos.pl")
	config["studentDashboardUrl"] = env("WORKER_DASHBOARD_URL", "/student/dashboard")
	configJSON, err = json.Marshal(config)
	if err != nil {
		return 0, err
	}
	bin, err := exec.LookPath(x.worker)
	if err != nil {
		return 0, errors.New("The worker binary is unavailable. Ask the administrator to check WORKER_BIN.")
	}
	parent := x.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.MaxRunSeconds)*time.Second)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(configJSON)
	cmd.Stderr = io.Discard
	// Do not inherit server secrets or LINGOS_* overrides into a user's worker.
	cmd.Env = []string{}
	for _, k := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return 0, errors.New("Could not open worker output.")
	}
	x.runMu.Lock()
	defer x.runMu.Unlock()
	// Holding workerMu proves that no live worker owns a leftover running record.
	if _, err := x.db.Exec(`UPDATE runs SET status='interrupted',summary='The previous run could not save its final status. Check Lingos before retrying.',finished_at=? WHERE status='running'`, time.Now().Unix()); err != nil {
		cancel()
		out.Close()
		return 0, errRunStorage
	}
	result, err := x.db.Exec(`INSERT INTO runs(user_id,mode,class_id,source,status,created_at,scheduled_for)
		SELECT id,?,?,?,'running',?,? FROM users WHERE id=? AND disabled=0`, mode, id, source, time.Now().Unix(), due, userID)
	if err != nil {
		cancel()
		out.Close()
		return 0, errRunStorage
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		cancel()
		out.Close()
		return 0, errors.New("This account is not available.")
	}
	runID, err := result.LastInsertId()
	if err != nil {
		cancel()
		out.Close()
		return 0, err
	}
	waitWorker, err := startWorkerProcess(cmd)
	if err != nil {
		cancel()
		out.Close()
		x.finishRun(runID, "failed", "The worker could not start. Check the server configuration.")
		// A durable failed attempt is not replayed: even an uncertain launch must be visible.
		return runID, nil
	}
	if x.cancels == nil {
		x.cancels = make(map[int64]context.CancelFunc)
	}
	x.cancels[runID] = cancel
	x.workers.Add(1)
	started = true
	go x.collectRun(ctx, cancel, waitWorker, out, runID, userID, mode, id)
	return runID, nil
}

func (x *app) cancelUserRuns(userID int64) error {
	x.runMu.Lock()
	defer x.runMu.Unlock()
	rows, err := x.db.Query(`SELECT id FROM runs WHERE user_id=? AND status='running'`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if cancel := x.cancels[id]; cancel != nil {
			cancel()
		}
	}
	return rows.Err()
}

func (x *app) collectRun(ctx context.Context, cancel context.CancelFunc, waitWorker func() error, out io.ReadCloser, runID, userID int64, mode, class string) {
	defer x.workers.Done()
	defer x.workerMu.Unlock()
	defer cancel()
	defer func() {
		x.runMu.Lock()
		delete(x.cancels, runID)
		x.runMu.Unlock()
	}()
	status, summary := "failed", "The worker ended without a result."
	internalFailure := false
	resultSaved := false
	scanner := bufio.NewScanner(io.LimitReader(out, 100<<20))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	seq := 0
	for scanner.Scan() {
		var event map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event == nil {
			status, summary = "failed", "The worker returned invalid output."
			internalFailure = true
			cancel()
			break
		}
		var kind string
		json.Unmarshal(event["type"], &kind)
		if kind == "result" {
			if resultSaved {
				status, summary, internalFailure = "failed", "The worker returned more than one result.", true
				cancel()
				break
			}
			var success bool
			json.Unmarshal(event["success"], &success)
			if success {
				status, summary = "completed", "Task completed."
				var err error
				switch mode {
				case "classes":
					err = x.saveClasses(userID, event["classes"])
					summary = "Classes updated."
				case "scrape":
					err = x.saveVocabulary(userID, class, event["scrape"])
					summary = "Vocabulary saved to your library."
				case "run":
					var lessons, words int
					json.Unmarshal(event["lessons_completed"], &lessons)
					json.Unmarshal(event["words_learned"], &words)
					summary = fmt.Sprintf("%d lessons completed · %d words learned", lessons, words)
				}
				if err != nil {
					status, summary = "failed", "The worker finished, but its result could not be saved."
					internalFailure = true
				}
			} else {
				status, summary = "failed", "The worker reported an error. Open the run for details."
			}
			// Large dictionaries live in the library, not duplicated in every event response.
			delete(event, "scrape")
			delete(event, "classes")
			delete(event, "username")
		}
		data, err := json.Marshal(event)
		if err != nil {
			cancel()
			status, summary = "failed", "Could not encode worker progress."
			internalFailure = true
			break
		}
		seq++
		if _, err := x.db.Exec(`INSERT INTO run_events(run_id,seq,data) VALUES(?,?,?)`, runID, seq, string(data)); err != nil {
			cancel()
			status, summary = "failed", "Could not save worker progress. Check server storage."
			internalFailure = true
			break
		}
		if kind == "result" {
			resultSaved = true
		}
		// shortcut: retain the latest 5000 events per run; use archival storage if full HTTP traces become necessary.
		if seq%100 == 0 && seq > 5000 {
			if _, err := x.db.Exec(`DELETE FROM run_events WHERE run_id=? AND seq<=?`, runID, seq-5000); err != nil {
				cancel()
				status, summary = "failed", "Could not maintain the run log. Check server storage."
				internalFailure = true
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		cancel()
		status, summary = "failed", "Worker output was too large or could not be read."
		internalFailure = true
	}
	waitErr := waitWorker()
	if ctx.Err() != nil && !internalFailure && !resultSaved {
		status, summary = "cancelled", "Task stopped. Completed Lingos lessons cannot be undone."
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, summary = "failed", "The maximum run duration was reached."
		} else if x.ctx != nil && x.ctx.Err() != nil {
			status, summary = "interrupted", "The server stopped during this task. Check Lingos before retrying."
		}
	} else if waitErr != nil && !internalFailure && !resultSaved {
		status, summary = "failed", "The worker exited with an error. Open the run for details."
	}
	x.finishRun(runID, status, summary)
}

func (x *app) finishRun(id int64, status, summary string) {
	if _, err := x.db.Exec(`UPDATE runs SET status=?,summary=?,finished_at=? WHERE id=?`, status, summary, time.Now().Unix(), id); err != nil {
		// The next launch (or startup) recovers any remaining running record.
		fmt.Fprintf(os.Stderr, "save run %d result: %v\n", id, err)
	}
}

const runColumns = `id,user_id,mode,class_id,source,status,summary,created_at,finished_at,scheduled_for`

func scanRun(row interface{ Scan(...any) error }) (RunRecord, error) {
	var r RunRecord
	err := row.Scan(&r.ID, &r.UserID, &r.Mode, &r.ClassID, &r.Source, &r.Status, &r.Summary, &r.CreatedAt, &r.FinishedAt, &r.ScheduledFor)
	return r, err
}

func (x *app) runsAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if r.URL.Path == "/api/runs" {
		switch r.Method {
		case "POST":
			var in struct {
				Mode        string          `json:"mode"`
				ClassID     string          `json:"class_id"`
				LessonCount json.RawMessage `json:"lesson_count"`
			}
			if !decodeJSON(w, r, &in) {
				return
			}
			var lessonCount *int
			// RawMessage distinguishes omission (saved count) from an explicit null.
			if in.LessonCount != nil && (json.Unmarshal(in.LessonCount, &lessonCount) != nil || lessonCount == nil) {
				apiError(w, 400, "lesson_count must be an integer between 1 and 10000.")
				return
			}
			id, err := x.launchRun(u.ID, in.Mode, in.ClassID, "manual", nil, lessonCount)
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, errWorkerBusy) {
					status = http.StatusConflict
				} else if errors.Is(err, errRunStorage) {
					status = http.StatusInternalServerError
				}
				apiError(w, status, err.Error())
				return
			}
			run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=? AND user_id=?`, id, u.ID))
			if err != nil {
				apiError(w, 500, "Could not load the new run.")
				return
			}
			writeJSON(w, 201, map[string]any{"run": run})
		case "GET":
			rows, err := x.db.Query(`SELECT `+runColumns+` FROM runs WHERE user_id=? ORDER BY id DESC LIMIT 50`, u.ID)
			if err != nil {
				apiError(w, 500, "Could not load runs.")
				return
			}
			defer rows.Close()
			runs := []RunRecord{}
			for rows.Next() {
				run, err := scanRun(rows)
				if err != nil {
					apiError(w, 500, "Could not read run history.")
					return
				}
				runs = append(runs, run)
			}
			if rows.Err() != nil {
				apiError(w, 500, "Could not read run history.")
				return
			}
			writeJSON(w, 200, map[string]any{"runs": runs})
		default:
			apiError(w, 405, "Method not allowed.")
		}
		return
	}
	if r.URL.Path == "/api/runs/latest" {
		if r.Method != "GET" {
			apiError(w, 405, "Method not allowed.")
			return
		}
		run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE user_id=? AND mode='run' AND status!='running' ORDER BY id DESC LIMIT 1`, u.ID))
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 200, map[string]any{"run": nil, "result": nil})
			return
		}
		if err != nil {
			apiError(w, 500, "Could not load the latest lesson run.")
			return
		}
		var data string
		err = x.db.QueryRow(`SELECT data FROM run_events WHERE run_id=?
			AND CASE WHEN json_valid(data) THEN json_extract(data,'$.type') END = 'result'
			ORDER BY seq DESC LIMIT 1`, run.ID).Scan(&data)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, "Could not load the latest lesson result.")
			return
		}
		var result map[string]json.RawMessage
		if err == nil {
			if json.Unmarshal([]byte(data), &result) != nil {
				apiError(w, 500, "Could not read the latest lesson result.")
				return
			}
			// Legacy events may predate collectRun's payload stripping.
			for _, key := range []string{"username", "scrape", "classes", "credentials"} {
				delete(result, key)
			}
		}
		writeJSON(w, 200, map[string]any{"run": run, "result": result})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || len(parts) > 2 || (len(parts) == 2 && parts[1] != "cancel") {
		apiError(w, 404, "Run not found.")
		return
	}
	run, err := scanRun(x.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id=? AND user_id=?`, id, u.ID))
	if err != nil {
		apiError(w, 404, "Run not found.")
		return
	}
	if len(parts) == 2 {
		if r.Method != "POST" {
			apiError(w, 405, "Use POST to stop a task.")
			return
		}
		x.runMu.Lock()
		cancel := x.cancels[id]
		if cancel != nil {
			cancel()
		}
		x.runMu.Unlock()
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "GET" {
		apiError(w, 405, "Method not allowed.")
		return
	}
	after := int64(0)
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = strconv.ParseInt(v, 10, 64)
		if err != nil || after < 0 {
			apiError(w, 400, "Invalid event cursor.")
			return
		}
	}
	rows, err := x.db.Query(`SELECT seq,data FROM run_events WHERE run_id=? AND seq>? ORDER BY seq LIMIT 201`, id, after)
	if err != nil {
		apiError(w, 500, "Could not read task progress.")
		return
	}
	defer rows.Close()
	type entry struct {
		Seq   int64           `json:"seq"`
		Event json.RawMessage `json:"event"`
	}
	events := []entry{}
	more := false
	for rows.Next() {
		var e entry
		var data string
		if err := rows.Scan(&e.Seq, &data); err != nil {
			apiError(w, 500, "Could not read task progress.")
			return
		}
		e.Event = json.RawMessage(data)
		if len(events) == 200 {
			more = true
			break
		}
		events = append(events, e)
		after = e.Seq
	}
	if err := rows.Close(); err != nil || rows.Err() != nil {
		apiError(w, 500, "Could not read task progress.")
		return
	}
	writeJSON(w, 200, map[string]any{"run": run, "events": events, "last_seq": after, "has_more": more})
}

type cachedClass struct {
	Title   string `json:"title"`
	GroupID string `json:"group_id"`
}

func (x *app) saveClasses(userID int64, raw json.RawMessage) error {
	var classes []cachedClass
	if err := json.Unmarshal(raw, &classes); err != nil || classes == nil {
		return errors.New("invalid class list")
	}
	for _, c := range classes {
		if _, err := workerArgs("scrape", c.GroupID); err != nil {
			return err
		}
	}
	_, err := x.db.Exec(`INSERT INTO class_cache(user_id,data) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET data=excluded.data`, userID, string(raw))
	return err
}

func (x *app) classesAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	var data string
	err := x.db.QueryRow(`SELECT data FROM class_cache WHERE user_id=?`, u.ID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		data = `[]`
	} else if err != nil {
		apiError(w, 500, "Could not load classes.")
		return
	}
	writeJSON(w, 200, map[string]any{"classes": json.RawMessage(data)})
}
