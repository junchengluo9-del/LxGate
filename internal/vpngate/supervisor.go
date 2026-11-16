package vpngate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// States reported by Status.
const (
	StateDisabled    = "disabled"
	StateUnavailable = "unavailable"
	StateStarting    = "starting"
	StateRunning     = "running"
	StateRestarting  = "restarting"
	StateStopped     = "stopped"
)

const (
	// restart backoff grows from 1s to 30s so a permanently broken engine is
	// not respawned in a tight loop.
	minRestartBackoff = time.Second
	maxRestartBackoff = 30 * time.Second
	// a run longer than this counts as healthy and resets the backoff.
	healthyRunThreshold = 2 * time.Minute
	// tailLines is how much engine output is kept in memory for the UI.
	tailLines = 200
	stopGrace  = 8 * time.Second
)

// Status is what the panel shows for the embedded engine.
type Status struct {
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	Running       bool   `json:"running"`
	PID           int    `json:"pid"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	Warning       string `json:"warning,omitempty"`
	Restarts      int    `json:"restarts"`
	APIPort       int    `json:"apiPort"`
	ProxyPort     int    `json:"proxyPort"`
	APIAddress    string `json:"apiAddress"`
	ProxyAddress  string `json:"proxyAddress"`
	OpenVPNDev    string `json:"openvpnDev"`
	SocksURL      string `json:"socksUrl"`
	HTTPProxyURL  string `json:"httpProxyUrl"`
	ScriptPath    string `json:"scriptPath"`
	PythonPath    string `json:"pythonPath"`
	DataDir       string `json:"dataDir"`
	StartedAt     string `json:"startedAt,omitempty"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	LastExit      string `json:"lastExit,omitempty"`
}

// Supervisor owns the engine process and its lifecycle.
type Supervisor struct {
	cfg    Config
	client *Client

	mu        sync.RWMutex
	state     string
	reason    string
	warning   string
	cmd       *exec.Cmd
	pid       int
	startedAt time.Time
	lastExit  string
	restarts  int
	logTail   []string

	cancel context.CancelFunc
	done   chan struct{}
}

// NewSupervisor builds a supervisor. Nothing starts until Start is called.
func NewSupervisor(cfg Config) *Supervisor {
	state := StateStopped
	if !cfg.Enabled {
		state = StateDisabled
	}
	return &Supervisor{
		cfg:    cfg,
		client: NewClient(cfg),
		state:  state,
		done:   make(chan struct{}),
	}
}

// Client returns the engine API client bound to this supervisor.
func (s *Supervisor) Client() *Client { return s.client }

// Config returns a copy of the supervisor configuration.
func (s *Supervisor) Config() Config { return s.cfg }

// Start launches the engine in the background and returns immediately: the
// panel must come up even when the engine can never run on this host.
func (s *Supervisor) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		s.setState(StateDisabled, "", "")
		close(s.done)
		return
	}
	if err := s.cfg.Validate(); err != nil {
		s.setState(StateUnavailable, err.Error(), "")
		close(s.done)
		logger.Warningf("vpngate: engine not started: %v", err)
		return
	}
	warning, err := s.cfg.CheckPrerequisites()
	if err != nil {
		s.setState(StateUnavailable, err.Error(), "")
		close(s.done)
		logger.Warningf("vpngate: engine not started: %v", err)
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	if warning != "" {
		s.warning = warning
	}
	s.state = StateStarting
	s.mu.Unlock()
	if warning != "" {
		logger.Warningf("vpngate: %s", warning)
	}

	go s.loop(runCtx)
}

// Stop terminates the engine and waits (bounded) for its process to exit.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-s.done:
	case <-time.After(stopGrace + 4*time.Second):
		logger.Warning("vpngate: engine did not stop in time")
	}
}

// Started reports whether the supervisor is running its lifecycle loop.
func (s *Supervisor) Started() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cancel != nil
}

// Status snapshots the current state.
func (s *Supervisor) Status() Status {
	s.mu.RLock()
	state := s.state
	reason := s.reason
	warning := s.warning
	pid := s.pid
	restarts := s.restarts
	startedAt := s.startedAt
	lastExit := s.lastExit
	s.mu.RUnlock()

	status := Status{
		Enabled:      s.cfg.Enabled,
		State:        state,
		Reason:       reason,
		Warning:      warning,
		Restarts:     restarts,
		APIPort:      s.cfg.APIPort,
		ProxyPort:    s.cfg.ProxyPort,
		APIAddress:   s.cfg.Address(),
		ProxyAddress: s.cfg.ProxyAddress(),
		OpenVPNDev:   s.cfg.OpenVPNDev,
		SocksURL:     "socks5://" + s.cfg.ProxyAddress(),
		HTTPProxyURL: "http://" + s.cfg.ProxyAddress(),
		ScriptPath:   s.cfg.ScriptPath,
		PythonPath:   s.cfg.PythonBin,
		DataDir:      s.cfg.DataDir,
		LastExit:     lastExit,
	}
	status.Available = state != StateDisabled && state != StateUnavailable
	status.Running = state == StateRunning && pid > 0
	status.PID = pid
	if !startedAt.IsZero() {
		status.StartedAt = startedAt.Format(time.RFC3339)
		if status.Running {
			status.UptimeSeconds = int64(time.Since(startedAt).Seconds())
		}
	}
	return status
}

// Tail returns the last lines of engine output kept in memory.
func (s *Supervisor) Tail() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.logTail))
	copy(out, s.logTail)
	return out
}

func (s *Supervisor) loop(ctx context.Context) {
	defer close(s.done)
	backoff := minRestartBackoff
	for {
		if ctx.Err() != nil {
			s.setState(StateStopped, "", s.currentWarning())
			return
		}
		started := time.Now()
		exitErr := s.runOnce(ctx)
		if ctx.Err() != nil {
			s.setState(StateStopped, "", s.currentWarning())
			return
		}

		runFor := time.Since(started)
		if runFor >= healthyRunThreshold {
			backoff = minRestartBackoff
		}
		exitText := "engine exited"
		if exitErr != nil {
			exitText = exitErr.Error()
		}
		s.mu.Lock()
		s.lastExit = exitText
		s.restarts++
		s.pid = 0
		s.cmd = nil
		s.state = StateRestarting
		s.reason = "engine exited; restarting"
		s.mu.Unlock()
		logger.Warningf("vpngate: %s (%s); restarting in %s", exitText, runFor.Round(time.Second), backoff)

		select {
		case <-ctx.Done():
			s.setState(StateStopped, "", s.currentWarning())
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxRestartBackoff {
			backoff = maxRestartBackoff
		}
	}
}

// runOnce starts the process, waits for the health probe, then blocks until the
// process exits or ctx is cancelled.
func (s *Supervisor) runOnce(ctx context.Context) error {
	cmd := exec.Command(s.cfg.PythonBin, s.cfg.ScriptPath) // #nosec G204 -- configured interpreter and repo-shipped script
	cmd.Dir = filepath.Dir(s.cfg.ScriptPath)
	cmd.Env = append(os.Environ(), s.childEnv()...)
	setSysProcAttr(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("engine stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.pid = cmd.Process.Pid
	s.startedAt = time.Now()
	s.state = StateStarting
	s.reason = ""
	s.logTail = nil
	s.mu.Unlock()

	go s.pump(stdout)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	// The process being up is not the same as the engine being usable: probe the
	// loopback API before calling it running, so the UI never reports a tunnel
	// that is not there.
	ready := make(chan error, 1)
	go s.waitReady(ctx, ready)

waitReady:
	for {
		select {
		case <-ctx.Done():
			terminate(cmd, waitErr, stopGrace)
			return nil
		case err := <-waitErr:
			return exitReason(err)
		case err := <-ready:
			if err != nil {
				s.setReason(err.Error())
			} else {
				s.setState(StateRunning, "", "")
			}
			break waitReady
		}
	}

	select {
	case <-ctx.Done():
		terminate(cmd, waitErr, stopGrace)
		return nil
	case err := <-waitErr:
		return exitReason(err)
	}
}

// waitReady polls the engine's health endpoint until it answers or the start
// timeout expires.
func (s *Supervisor) waitReady(ctx context.Context, out chan<- error) {
	timeout := s.cfg.StartTimeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			out <- ctx.Err()
			return
		}
		ctxProbe, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.client.Health(ctxProbe)
		cancel()
		if err == nil {
			out <- nil
			return
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("engine API did not become ready")
	}
	out <- fmt.Errorf("engine process is up but its API is not answering yet: %w", lastErr)
}

// exitReason normalizes a process exit into a short human-readable string.
func exitReason(err error) error {
	if err == nil {
		return errors.New("engine exited with status 0")
	}
	return err
}

// childEnv is the embedded contract: loopback-only bindings, the shared token,
// and the state directory the Go side provisions.
func (s *Supervisor) childEnv() []string {
	return []string{
		"LXGATE_EMBEDDED=1",
		"LXGATE_API_TOKEN=" + s.cfg.Token,
		"LXGATE_VPNGATE_API_PORT=" + strconv.Itoa(s.cfg.APIPort),
		"UI_HOST=127.0.0.1",
		"UI_PORT=" + strconv.Itoa(s.cfg.APIPort),
		"LOCAL_PROXY_HOST=127.0.0.1",
		"LOCAL_PROXY_PORT=" + strconv.Itoa(s.cfg.ProxyPort),
		"LXGATE_OPENVPN_DEV=" + s.cfg.OpenVPNDev,
		"VPNGATE_DATA_DIR=" + s.cfg.DataDir,
		"PYTHONUNBUFFERED=1",
		// The engine's own git-based self-update is meaningless once embedded;
		// LxGate updates it together with the panel.
		"LXGATE_DISABLE_SELF_UPDATE=1",
	}
}

func (s *Supervisor) pump(reader io.Reader) {
	buf := make([]byte, 4096)
	var carry string
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			carry += string(buf[:n])
			for {
				idx := strings.IndexByte(carry, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(carry[:idx])
				carry = carry[idx+1:]
				if line != "" {
					s.appendLine(line)
				}
			}
			if len(carry) > 4096 {
				s.appendLine(strings.TrimSpace(carry))
				carry = ""
			}
		}
		if err != nil {
			if strings.TrimSpace(carry) != "" {
				s.appendLine(strings.TrimSpace(carry))
			}
			return
		}
	}
}

func (s *Supervisor) appendLine(line string) {
	s.mu.Lock()
	s.logTail = append(s.logTail, line)
	if len(s.logTail) > tailLines {
		s.logTail = s.logTail[len(s.logTail)-tailLines:]
	}
	s.mu.Unlock()
}

func (s *Supervisor) setReason(reason string) {
	s.mu.Lock()
	s.reason = reason
	s.mu.Unlock()
}

func (s *Supervisor) setState(state, reason, warning string) {
	s.mu.Lock()
	s.state = state
	s.reason = reason
	if warning != "" || state == StateStopped {
		s.warning = warning
	}
	s.mu.Unlock()
}

func (s *Supervisor) currentWarning() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.warning
}
