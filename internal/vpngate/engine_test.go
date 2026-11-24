package vpngate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	engineassets "github.com/mhsanaei/3x-ui/v3/vpngate"
)

// newEngineTestConfig points the engine at a throwaway directory that stands in
// for /opt/lxgate/vpngate.
func newEngineTestConfig(t *testing.T) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	resetEngineEnsureCache()
	t.Cleanup(resetEngineEnsureCache)
	cfg := &Config{
		Enabled:    true,
		PythonBin:  "python3",
		ScriptPath: filepath.Join(dir, managerScript),
	}
	return cfg, dir
}

// The field failure: a fresh install whose archive (or raw download) never
// delivered vpngate_manager.py. The panel must write the engine it embeds.
func TestEnsureEngineWritesMissingScripts(t *testing.T) {
	cfg, dir := newEngineTestConfig(t)

	report := cfg.EnsureEngine()
	if report.Skipped != "" {
		t.Fatalf("an empty engine directory must be filled, not skipped: %s", report.Skipped)
	}
	if !report.Changed {
		t.Fatal("a missing engine must be reported as written")
	}
	if report.Version != engineassets.Version() {
		t.Fatalf("report version = %q, want %q", report.Version, engineassets.Version())
	}
	for _, name := range engineassets.Names {
		path := filepath.Join(dir, name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		want, _ := engineassets.Read(name)
		if string(got) != string(want) {
			t.Fatalf("%s content differs from the embedded copy", name)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if perm := info.Mode().Perm(); perm != engineFileMode {
			t.Fatalf("%s mode = %o, want %o", name, perm, engineFileMode)
		}
	}
	stamp, err := os.ReadFile(filepath.Join(dir, engineVersionFile))
	if err != nil {
		t.Fatalf("version stamp missing: %v", err)
	}
	if strings.TrimSpace(string(stamp)) != engineassets.Version() {
		t.Fatalf("stamp = %q, want %q", stamp, engineassets.Version())
	}
	// The prerequisites check the supervisor runs next must now pass with only
	// the interpreter as an unknown (python3 may be absent in a bare sandbox).
	if _, err := cfg.CheckPrerequisites(); err != nil && !strings.Contains(err.Error(), "python3") {
		t.Fatalf("engine still not usable after EnsureEngine: %v", err)
	}
}

// A second boot must not rewrite anything: the stamp says the copy is current.
func TestEnsureEngineIsIdempotent(t *testing.T) {
	cfg, dir := newEngineTestConfig(t)
	if report := cfg.EnsureEngine(); !report.Changed {
		t.Fatalf("first call must write the engine: %s", report.Skipped)
	}
	path := filepath.Join(dir, managerScript)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	resetEngineEnsureCache() // a restart forgets the in-process memo
	report := cfg.EnsureEngine()
	if report.Changed {
		t.Fatalf("a current engine must not be rewritten, wrote %v", report.Written)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the manager script was touched without a content change")
	}
}

// An engine left behind by an older build (an update that only swapped the
// binary) is replaced with the embedded one.
func TestEnsureEngineReplacesStaleScripts(t *testing.T) {
	cfg, dir := newEngineTestConfig(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, managerScript), []byte("# old engine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, engineVersionFile), []byte("deadbeefdeadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := cfg.EnsureEngine()
	if !report.Changed {
		t.Fatalf("a stale engine must be refreshed: %s", report.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dir, managerScript))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := engineassets.Read(managerScript)
	if string(got) != string(want) {
		t.Fatal("the stale manager script was not replaced")
	}
	if !contains(report.Written, managerScript) {
		t.Fatalf("written = %v, want it to name %s", report.Written, managerScript)
	}
}

// A stamp that looks current but a file that was deleted or truncated by hand
// is caught by the size check.
func TestEnsureEngineRepairsDeletedAndTruncatedFiles(t *testing.T) {
	cfg, dir := newEngineTestConfig(t)
	cfg.EnsureEngine()

	if err := os.Remove(filepath.Join(dir, "vpn_utils.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy_server.py"), []byte("cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetEngineEnsureCache()

	report := cfg.EnsureEngine()
	if !report.Changed {
		t.Fatalf("an incomplete engine must be repaired: %s", report.Skipped)
	}
	if !contains(report.Written, "vpn_utils.py") || !contains(report.Written, "proxy_server.py") {
		t.Fatalf("written = %v, want both damaged files", report.Written)
	}
	for _, name := range []string{"vpn_utils.py", "proxy_server.py"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s not restored: %v", name, err)
		}
		want, _ := engineassets.Read(name)
		if string(data) != string(want) {
			t.Fatalf("%s was not restored to the embedded copy", name)
		}
	}
}

// The engine directory belongs to the panel: a script edited by hand is put back
// to the embedded copy, because the panel cannot tell a local patch from a
// half-written file and the engine has to match the binary that supervises it.
func TestEnsureEngineRepairsEditedScript(t *testing.T) {
	cfg, dir := newEngineTestConfig(t)
	cfg.EnsureEngine()

	edited := []byte("# local patch that changes the size\n")
	path := filepath.Join(dir, "vpn_utils.py")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	resetEngineEnsureCache()

	report := cfg.EnsureEngine()
	if !report.Changed || !contains(report.Written, "vpn_utils.py") {
		t.Fatalf("an edited engine script must be restored, wrote %v", report.Written)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := engineassets.Read("vpn_utils.py")
	if string(got) != string(want) {
		t.Fatal("the edited script was not restored to the embedded copy")
	}
}

// A read-only engine directory (a checkout, or a container path owned by the
// image) is reported as skipped: it must not look like a broken install.
func TestEnsureEngineReadOnlyDirectoryIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	resetEngineEnsureCache()
	t.Cleanup(resetEngineEnsureCache)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	cfg := &Config{Enabled: true, PythonBin: "python3", ScriptPath: filepath.Join(dir, managerScript)}
	report := cfg.EnsureEngine()
	if report.Changed {
		t.Fatal("nothing can be written to a read-only directory")
	}
	if report.Skipped == "" {
		t.Fatal("a read-only engine directory must be reported")
	}
}

// The engine is a sidecar of the panel, not a requirement: a disabled engine
// does no disk work at all.
func TestEnsureEngineDisabledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Enabled: false, ScriptPath: filepath.Join(dir, managerScript)}
	report := cfg.EnsureEngine()
	if report.Changed {
		t.Fatal("a disabled engine must not write anything")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("directory not empty: %v %v", entries, err)
	}
}

// A script configured somewhere else (an operator-set LXGATE_VPNGATE_SCRIPT in
// a checkout) is refreshed in place, so the running engine always matches the
// binary that supervises it.
func TestEnsureEngineUsesConfiguredScriptDirectory(t *testing.T) {
	home := t.TempDir()
	checkout := filepath.Join(home, "vpngate")
	resetEngineEnsureCache()
	t.Cleanup(resetEngineEnsureCache)

	cfg := &Config{Enabled: true, PythonBin: "python3", ScriptPath: filepath.Join(checkout, managerScript)}
	report := cfg.EnsureEngine()
	if report.Dir != checkout {
		t.Fatalf("dir = %q, want %q", report.Dir, checkout)
	}
	if _, err := os.Stat(filepath.Join(checkout, managerScript)); err != nil {
		t.Fatalf("engine not written to the configured directory: %v", err)
	}
}

// The embedded version is a hash of the sources, so a changed script changes it
// and two calls in one build agree.
func TestEmbeddedEngineVersionIsStable(t *testing.T) {
	first := engineassets.Version()
	if len(first) != 16 {
		t.Fatalf("version = %q, want 16 hex characters", first)
	}
	if second := engineassets.Version(); second != first {
		t.Fatalf("version changed between calls: %q != %q", first, second)
	}
	if len(engineassets.Names) != 3 {
		t.Fatalf("expected the three runtime scripts, got %v", engineassets.Names)
	}
	for _, name := range engineassets.Names {
		data, err := engineassets.Read(name)
		if err != nil {
			t.Fatalf("%s is not embedded: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	if data, err := engineassets.Read("tests/app_update_test.py"); err == nil && len(data) > 0 {
		t.Fatal("the engine tests must not be embedded")
	}
}

// A config with no script path still targets the documented install directory
// instead of the working directory.
func TestEngineDirDefaultsToInstallDir(t *testing.T) {
	cfg := Config{}
	if got, want := cfg.EngineDir(), filepath.Join(DefaultInstallDir, "vpngate"); got != want {
		t.Fatalf("EngineDir() = %q, want %q", got, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
