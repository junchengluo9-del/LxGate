//go:build windows

package vpngate

import (
	"os/exec"
	"time"
)

func setSysProcAttr(_ *exec.Cmd) {}

func terminate(cmd *exec.Cmd, wait <-chan error, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-wait:
	case <-time.After(grace):
	}
}
