//go:build !linux

package webui

import "os/exec"

func startWorkerProcess(cmd *exec.Cmd) (func() error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Wait, nil
}
