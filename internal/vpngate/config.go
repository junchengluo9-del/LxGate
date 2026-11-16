// Package vpngate embeds and supervises the Python VPNGate engine that ships
// with LxGate (vpngate/vpngate_manager.py), and provides the HTTP client the
// panel uses to drive it.
//
// The engine is a sidecar: it owns the OpenVPN tunnel on tun0 plus a local
// SOCKS5/HTTP proxy, and it is only ever reachable from 127.0.0.1. LxGate's Go
// backend starts it, watches it, and proxies every panel call through the
// authenticated API group, so the engine's own web UI is never exposed.
package vpngate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Defaults mirror the documented LxGate layout. The API port is internal-only
// (127.0.0.1) and deliberately different from the standalone engine's 8787 so
// that an accidentally exposed engine cannot collide with the panel.
const (
	DefaultAPIPort    = 18787
	DefaultProxyPort  = 7928
	// DefaultPythonBin is the interpreter the sidecar runs on.
	DefaultPythonBin = "python3"
	// DefaultOpenVPNDev is the tunnel device the engine asks openvpn for. A host
	// where another OpenVPN manager already owns tun0 sets LXGATE_OPENVPN_DEV to
	// a free name (a second openvpn on a busy tun device fails with EBUSY).
	DefaultOpenVPNDev      = "tun0"
	DefaultDataDir    = "/opt/lxgate/vpngate_data"
	DefaultInstallDir = "/opt/lxgate"

	// tokenHeader carries the shared secret between the Go backend and the
	// engine. The engine only accepts requests that present it while running in
	// embedded mode.
	tokenHeader = "X-LxGate-Token"

	defaultStartTimeout = 45 * time.Second
	defaultAPITimeout   = 20 * time.Second
)

// Config describes one embedded engine instance.
type Config struct {
	// Enabled gates the whole sidecar. LXGATE_VPNGATE_ENABLED=0 turns it off,
	// which keeps the panel usable on hosts where OpenVPN cannot run at all.
	Enabled    bool
	PythonBin  string
	ScriptPath string
	DataDir    string
	APIPort    int
	ProxyPort  int
	// OpenVPNDev is the tunnel interface name handed to the engine. It never
	// changes the panel's own ports; it only keeps two tunnel managers on one
	// host from fighting over tun0.
	OpenVPNDev string
	Token      string
	// APITimeout bounds one request to the engine's HTTP API.
	APITimeout time.Duration
	// StartTimeout bounds how long Start waits for the engine to answer its
	// health probe before reporting a failure.
	StartTimeout time.Duration
}

// LoadConfig reads the engine configuration from the environment, falling back
// to paths derived from the running binary.
func LoadConfig() Config {
	cfg := Config{
		Enabled:      envBool("LXGATE_VPNGATE_ENABLED", true),
		PythonBin:    firstNonEmpty(os.Getenv("LXGATE_PYTHON"), os.Getenv("VPNGATE_PYTHON"), DefaultPythonBin),
		ScriptPath:   os.Getenv("LXGATE_VPNGATE_SCRIPT"),
		DataDir:      firstNonEmpty(os.Getenv("LXGATE_VPNGATE_DATA_DIR"), os.Getenv("VPNGATE_DATA_DIR")),
		APIPort:      envInt("LXGATE_VPNGATE_API_PORT", DefaultAPIPort),
		ProxyPort:    envInt("LOCAL_PROXY_PORT", DefaultProxyPort),
		OpenVPNDev:   firstNonEmpty(os.Getenv("LXGATE_OPENVPN_DEV"), DefaultOpenVPNDev),
		Token:        strings.TrimSpace(os.Getenv("LXGATE_VPNGATE_TOKEN")),
		APITimeout:   defaultAPITimeout,
		StartTimeout: defaultStartTimeout,
	}
	if cfg.ScriptPath == "" {
		cfg.ScriptPath = resolveScriptPath()
	}
	if cfg.DataDir == "" {
		cfg.DataDir = resolveDataDir()
	}
	if cfg.Token == "" {
		cfg.Token = loadOrCreateToken(cfg.DataDir)
	}
	return cfg
}

// Address returns the loopback address the engine's internal API listens on.
func (c Config) Address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.APIPort))
}

// BaseURL is the internal API root used by the client.
func (c Config) BaseURL() string {
	return "http://" + c.Address()
}

// ProxyAddress is the local SOCKS5/HTTP proxy a Xray outbound points at.
func (c Config) ProxyAddress() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.ProxyPort))
}

// Validate reports the reasons the engine cannot be started. A nil error only
// means the configuration is complete; the binaries are checked separately so a
// dev box without OpenVPN still gets a clear status instead of a crash.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	var problems []string
	if c.APIPort <= 0 || c.APIPort > 65535 {
		problems = append(problems, fmt.Sprintf("invalid internal API port %d", c.APIPort))
	}
	if c.ProxyPort <= 0 || c.ProxyPort > 65535 {
		problems = append(problems, fmt.Sprintf("invalid proxy port %d", c.ProxyPort))
	}
	if c.APIPort == c.ProxyPort {
		problems = append(problems, "internal API port and proxy port must differ")
	}
	if strings.TrimSpace(c.Token) == "" {
		problems = append(problems, "empty engine API token")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// CheckPrerequisites reports the binaries the engine needs. The returned
// warning is non-fatal (the engine still runs without OpenVPN, it just cannot
// connect), while the error means the engine cannot be started at all.
func (c Config) CheckPrerequisites() (warning string, err error) {
	if !c.Enabled {
		return "", nil
	}
	if _, lookErr := exec.LookPath(c.PythonBin); lookErr != nil {
		return "", fmt.Errorf("python3 (%s) not found: %w", c.PythonBin, lookErr)
	}
	info, statErr := os.Stat(c.ScriptPath)
	if statErr != nil {
		return "", fmt.Errorf("VPNGate engine script missing: %w", statErr)
	}
	if info.IsDir() {
		return "", fmt.Errorf("VPNGate engine script path is a directory: %s", c.ScriptPath)
	}
	if _, lookErr := exec.LookPath("openvpn"); lookErr != nil {
		warning = "openvpn is not installed: the engine runs and lists nodes, but connecting will fail until openvpn is added"
	}
	return warning, nil
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > 65535 {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// resolveScriptPath finds vpngate/vpngate_manager.py for an installed layout
// first, then for a checkout (binary in the repo root, or this source tree).
func resolveScriptPath() string {
	candidates := []string{
		filepath.Join(DefaultInstallDir, "vpngate", "vpngate_manager.py"),
		"/etc/lx-gate/vpngate/vpngate_manager.py",
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "vpngate", "vpngate_manager.py"),
			filepath.Join(dir, "..", "vpngate", "vpngate_manager.py"),
		)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "vpngate", "vpngate_manager.py"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "vpngate", "vpngate_manager.py"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if abs, absErr := filepath.Abs(candidate); absErr == nil {
				return abs
			}
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(DefaultInstallDir, "vpngate", "vpngate_manager.py")
}

// resolveDataDir keeps engine state (nodes, blacklist, tunnel configs, logs)
// next to the install by default, and falls back to a checkout-local directory
// when /opt is not writable (dev runs and tests).
func resolveDataDir() string {
	if canWrite(filepath.Dir(DefaultDataDir)) {
		return DefaultDataDir
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Join(filepath.Dir(file), "..", "..", "vpngate_data")
	}
	return filepath.Join(os.TempDir(), "lxgate-vpngate-data")
}

func canWrite(dir string) bool {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		probe, err := os.CreateTemp(dir, ".lxgate-write-*")
		if err != nil {
			return false
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return true
	}
	return false
}

// loadOrCreateToken keeps the shared secret stable across restarts, so a
// running engine keeps trusting the panel after a panel-only restart.
func loadOrCreateToken(dataDir string) string {
	if strings.TrimSpace(dataDir) != "" {
		path := filepath.Join(dataDir, ".api_token")
		if raw, err := os.ReadFile(path); err == nil {
			if token := strings.TrimSpace(string(raw)); len(token) >= 16 {
				return token
			}
		}
		if err := os.MkdirAll(dataDir, 0o750); err == nil {
			token := randomToken()
			if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err == nil {
				return token
			}
		}
	}
	return randomToken()
}

func randomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}
