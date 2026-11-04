package webui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerProcessHelper(t *testing.T) {
	switch os.Getenv("LINGOS_TEST_WORKER_PROCESS") {
	case "parent":
		cmd := exec.CommandContext(context.Background(), "/bin/sleep", "60")
		wait, err := startWorkerProcess(cmd)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			cmd.Process.Kill()
			wait()
		}()
		if err := os.WriteFile(os.Getenv("LINGOS_TEST_WORKER_PID"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		t.Fatal("parent did not kill the helper")
	case "stdout":
		if _, err := io.WriteString(os.Stdout, strings.Repeat("worker-output\n", 256)); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
}

func TestWorkerProcessDiesWithParent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pidFile := filepath.Join(t.TempDir(), "worker.pid")
	helper := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerProcessHelper$")
	helper.Env = append(os.Environ(), "LINGOS_TEST_WORKER_PROCESS=parent", "LINGOS_TEST_WORKER_PID="+pidFile)
	var helperOutput strings.Builder
	helper.Stdout, helper.Stderr = &helperOutput, &helperOutput
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	workerPID := 0
	helperWaited := false
	t.Cleanup(func() {
		helper.Process.Kill()
		if !helperWaited {
			helper.Wait()
		}
		if workerPID == 0 {
			data, _ := os.ReadFile(pidFile)
			workerPID, _ = strconv.Atoi(string(data))
		}
		if workerPID > 0 {
			syscall.Kill(workerPID, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for workerPID == 0 {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(data) > 0 {
			workerPID, err = strconv.Atoi(string(data))
			if err != nil || workerPID <= 0 {
				t.Fatalf("invalid worker PID %q: %v", data, err)
			}
			break
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not report worker PID")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if workerProcessStopped(t, workerPID) {
		t.Fatal("worker exited before its parent was killed")
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helperWaited = true
	if err := helper.Wait(); err == nil {
		t.Fatalf("helper exited successfully instead of being killed: %s", &helperOutput)
	}
	deadline = time.Now().Add(2 * time.Second)
	for !workerProcessStopped(t, workerPID) {
		if time.Now().After(deadline) {
			t.Fatalf("worker %d survived SIGKILL of its parent", workerPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerProcessReadsStdoutBeforeWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerProcessHelper$")
	cmd.Env = append(os.Environ(), "LINGOS_TEST_WORKER_PROCESS=stdout")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	wait, err := startWorkerProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		cmd.Process.Kill()
		if !waited {
			wait()
		}
	})
	// Let the child exit with unread output, exposing any premature cmd.Wait.
	deadline := time.Now().Add(2 * time.Second)
	for !workerProcessStopped(t, cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("stdout helper did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, readErr := io.ReadAll(out)
	waited = true
	waitErr := wait()
	want := strings.Repeat("worker-output\n", 256)
	if readErr != nil || string(got) != want {
		t.Fatalf("stdout: got %d bytes, want %d; read error: %v", len(got), len(want), readErr)
	}
	if waitErr != nil {
		t.Fatal(waitErr)
	}
}

func workerProcessStopped(t *testing.T, pid int) bool {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
	if len(fields) == 0 {
		t.Fatalf("invalid process stat: %q", stat)
	}
	return fields[0] == "Z" || fields[0] == "X"
}
