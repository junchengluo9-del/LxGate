package vpngate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("LXGATE_VPNGATE_ENABLED", "")
	t.Setenv("LXGATE_VPNGATE_API_PORT", "")
	t.Setenv("LOCAL_PROXY_PORT", "")
	t.Setenv("LXGATE_VPNGATE_TOKEN", "")

	cfg := LoadConfig()
	if !cfg.Enabled {
		t.Fatal("engine must be enabled by default")
	}
	if cfg.APIPort != DefaultAPIPort {
		t.Fatalf("api port = %d, want %d", cfg.APIPort, DefaultAPIPort)
	}
	if cfg.ProxyPort != DefaultProxyPort {
		t.Fatalf("proxy port = %d, want %d", cfg.ProxyPort, DefaultProxyPort)
	}
	if got, want := cfg.Address(), "127.0.0.1:18787"; got != want {
		t.Fatalf("api address = %q, want %q", got, want)
	}
	if got, want := cfg.ProxyAddress(), "127.0.0.1:7928"; got != want {
		t.Fatalf("proxy address = %q, want %q", got, want)
	}
	// The API must never bind a public interface.
	if strings.HasPrefix(cfg.Address(), "0.0.0.0") {
		t.Fatal("engine API must bind loopback only")
	}
	if len(cfg.Token) < 16 {
		t.Fatalf("token too short: %q", cfg.Token)
	}
}

func TestLoadConfigEnvironment(t *testing.T) {
	t.Setenv("LXGATE_VPNGATE_ENABLED", "0")
	t.Setenv("LXGATE_VPNGATE_API_PORT", "29999")
	t.Setenv("LOCAL_PROXY_PORT", "7999")
	t.Setenv("LXGATE_VPNGATE_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("LXGATE_VPNGATE_SCRIPT", "/tmp/engine.py")

	cfg := LoadConfig()
	if cfg.Enabled {
		t.Fatal("LXGATE_VPNGATE_ENABLED=0 must disable the engine")
	}
	if cfg.APIPort != 29999 || cfg.ProxyPort != 7999 {
		t.Fatalf("ports = %d/%d, want 29999/7999", cfg.APIPort, cfg.ProxyPort)
	}
	if cfg.ScriptPath != "/tmp/engine.py" {
		t.Fatalf("script = %q", cfg.ScriptPath)
	}
	if cfg.Token != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("token = %q", cfg.Token)
	}
}

func TestLoadConfigRejectsInvalidPorts(t *testing.T) {
	t.Setenv("LXGATE_VPNGATE_API_PORT", "not-a-port")
	t.Setenv("LOCAL_PROXY_PORT", "70000")
	cfg := LoadConfig()
	if cfg.APIPort != DefaultAPIPort {
		t.Fatalf("invalid api port must fall back, got %d", cfg.APIPort)
	}
	if cfg.ProxyPort != DefaultProxyPort {
		t.Fatalf("out-of-range proxy port must fall back, got %d", cfg.ProxyPort)
	}
}

func TestConfigValidate(t *testing.T) {
	base := Config{Enabled: true, APIPort: 18787, ProxyPort: 7928, Token: "0123456789abcdef"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	samePort := base
	samePort.ProxyPort = samePort.APIPort
	if err := samePort.Validate(); err == nil {
		t.Fatal("api and proxy port collision must be rejected")
	}
	noToken := base
	noToken.Token = "  "
	if err := noToken.Validate(); err == nil {
		t.Fatal("empty token must be rejected")
	}
	disabled := Config{Enabled: false, APIPort: -1, ProxyPort: -1}
	if err := disabled.Validate(); err != nil {
		t.Fatalf("a disabled engine must not need a valid config: %v", err)
	}
}

func TestCheckPrerequisitesMissingPython(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "vpngate_manager.py")
	if err := os.WriteFile(script, []byte("# stub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Enabled: true, PythonBin: filepath.Join(dir, "no-such-python"), ScriptPath: script}
	if _, err := cfg.CheckPrerequisites(); err == nil {
		t.Fatal("missing interpreter must be reported")
	}
}

func TestCheckPrerequisitesMissingScript(t *testing.T) {
	cfg := Config{Enabled: true, PythonBin: "python3", ScriptPath: filepath.Join(t.TempDir(), "nope.py")}
	if _, err := cfg.CheckPrerequisites(); err == nil {
		t.Fatal("missing engine script must be reported")
	}
}

func TestCheckPrerequisitesOpenVPNMissingIsWarning(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "vpngate_manager.py")
	if err := os.WriteFile(script, []byte("# stub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Enabled: true, PythonBin: "python3", ScriptPath: script}
	warning, err := cfg.CheckPrerequisites()
	if err != nil {
		// python3 may legitimately be absent in some CI images; the shape we care
		// about is "missing openvpn never fails the check".
		if strings.Contains(err.Error(), "openvpn") {
			t.Fatalf("missing openvpn must be a warning, not an error: %v", err)
		}
		t.Skipf("python3 unavailable: %v", err)
	}
	if warning != "" && !strings.Contains(warning, "openvpn") {
		t.Fatalf("unexpected warning: %q", warning)
	}
}

func TestTokenIsStableAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	first := loadOrCreateToken(dir)
	second := loadOrCreateToken(dir)
	if first != second {
		t.Fatalf("token changed between loads: %q != %q", first, second)
	}
	info, err := os.Stat(filepath.Join(dir, ".api_token"))
	if err != nil {
		t.Fatalf("token file missing: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token file is readable by others: %v", info.Mode().Perm())
	}
}
