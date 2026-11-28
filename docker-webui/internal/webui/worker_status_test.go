package webui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeWorkerVersion(t *testing.T) {
	dir := t.TempDir()
	const bin = "custom-worker"
	t.Setenv("PATH", dir)
	t.Setenv("WORKER_BIN", bin)
	t.Setenv("RELEASE_VERSION", "latest-prerelease")
	worker, lock, err := prepareWorker(context.Background())
	if err != nil || lock != nil || worker != bin {
		t.Fatalf("prepare custom worker = %q, %v, %v", worker, lock, err)
	}
	for _, tc := range []struct{ output, want string }{
		{"lingosbot 1.2.3-beta.1+build", "1.2.3-beta.1+build"},
		{"lingosbot cannot read /private/worker", ""},
		{"lingosbot 1.2.3" + strings.Repeat(" ", 256), ""},
	} {
		fixture := "#!/bin/sh\n[ \"$1\" = --version ] || exit 1\nprintf '%s\\n' '" + tc.output + "'\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(fixture), 0700); err != nil {
			t.Fatal(err)
		}
		if got := probeWorkerVersion(context.Background(), worker); got != tc.want {
			t.Errorf("probe output %q = %q, want %q", tc.output, got, tc.want)
		}
	}

	fixture := "#!/bin/sh\nprintf 'lingosbot 1.2.3\\n'\nexec /bin/sleep 10\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := probeWorkerVersion(ctx, worker); got != "" || ctx.Err() != context.DeadlineExceeded {
		t.Errorf("timed-out probe = %q, context error = %v", got, ctx.Err())
	}
}

func TestWorkerStatusAPI(t *testing.T) {
	x := newAuthTestApp(t)
	admin, _ := authTestSession(t, x, 1)
	user, _ := authTestSession(t, x, authTestUser(t, x, "learner@example.com", false))
	handler := x.routes().ServeHTTP
	for _, tc := range []struct {
		token, version string
		status         int
		body           string
	}{
		{"", "1.2.3", http.StatusUnauthorized, `{"error":"unauthorized"}`},
		{user, "1.2.3", http.StatusForbidden, `{"error":"forbidden"}`},
		{admin, "1.2.3", http.StatusOK, `{"version":"1.2.3"}`},
		{admin, "", http.StatusOK, `{"version":""}`},
	} {
		x.workerVersion = tc.version
		w := authTestRequest(handler, http.MethodGet, "/api/admin/worker-status", "", tc.token, "")
		if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.body {
			t.Errorf("worker status = %d %s, want %d %s", w.Code, w.Body, tc.status, tc.body)
		}
	}
}
