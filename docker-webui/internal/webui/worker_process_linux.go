package webui

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func startWorkerProcess(cmd *exec.Cmd) (func() error, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	started, done := make(chan error, 1), make(chan error, 1)
	wait := make(chan struct{})
	go func() {
		// Linux ties Pdeathsig to the creating thread; keep that thread alive until Wait.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-wait
			done <- cmd.Wait()
		}
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return func() error {
		close(wait)
		return <-done
	}, nil
}
