//go:build !windows

package vpngate

import (
	"os/exec"
	"syscall"
	"time"
)

// setSysProcAttr puts the engine in its own session so the OpenVPN process it
// spawns shares a process group we can signal as a unit.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminate asks the whole engine process group to stop, then kills it. wait is
// the channel carrying this process's cmd.Wait() result, so exactly one goroutine
// reaps the engine.
func terminate(cmd *exec.Cmd, wait <-chan error, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// A setsid child leads its own process group, so -pid reaches openvpn too.
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-wait:
	case <-time.After(grace):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-wait
	}
}
