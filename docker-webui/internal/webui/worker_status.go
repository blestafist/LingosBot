package webui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var workerVersionLine = regexp.MustCompile(`^lingosbot ([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)$`)

type workerVersionOutput struct{ buffer bytes.Buffer }

func (out *workerVersionOutput) Write(p []byte) (int, error) {
	if len(p) > 256-out.buffer.Len() {
		return 0, io.ErrShortWrite
	}
	return out.buffer.Write(p)
}

func probeWorkerVersion(parent context.Context, worker string) string {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, worker, "--version")
	// Keep PATH for custom wrappers, without passing server credentials.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.WaitDelay = 200 * time.Millisecond
	var out workerVersionOutput
	cmd.Stdout = &out
	wait, err := startWorkerProcess(cmd)
	if err != nil || wait() != nil {
		return ""
	}
	if match := workerVersionLine.FindStringSubmatch(strings.TrimSpace(out.buffer.String())); match != nil {
		return match[1]
	}
	return ""
}

func (x *app) workerStatusAPI(w http.ResponseWriter, r *http.Request) {
	actor, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	if !actor.Admin {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": x.workerVersion})
}
