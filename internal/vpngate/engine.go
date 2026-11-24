package vpngate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	engineassets "github.com/mhsanaei/3x-ui/v3/vpngate"
)

// The engine's Python sources are compiled into the panel binary (see the
// engineassets package, which embeds vpngate/*.py). At boot the panel writes
// them into the directory it runs the engine from whenever that copy is missing
// or belongs to a different build. That makes the engine independent of how the
// panel was installed:
//
//   - a tarball that did not ship vpngate/ (the field failure this guards),
//   - an update that replaced only the binary and left /opt/lxgate/vpngate
//     behind at an older version,
//   - an install host whose raw.githubusercontent.com was unreachable while
//     install.sh tried to fetch the scripts,
//   - an operator who deleted the scripts by hand.
//
// The stamp file records which build the files on disk came from, so an
// up-to-date copy costs one read and nothing is rewritten on a normal boot.

const (
	// engineVersionFile names the stamp that records which build wrote the
	// engine scripts currently on disk.
	engineVersionFile = ".lxgate-engine-version"
	// managerScript is the entry point the supervisor executes.
	managerScript = "vpngate_manager.py"
	// engineFileMode is what the scripts are written with: readable by the
	// panel's service user, never executable (python3 runs them as an
	// argument, and the engine needs no exec bit of its own).
	engineFileMode = 0o644
	engineDirMode  = 0o755
)

// EngineReport is the outcome of one EnsureEngine call. The panel logs it and
// the VPNGate status carries the version, so a version drift is visible instead
// of guessed at.
type EngineReport struct {
	// Dir is the engine directory the call looked at.
	Dir string
	// Version identifies the embedded sources this binary was built with.
	Version string
	// Changed reports whether anything was written.
	Changed bool
	// Written lists the files that were written or replaced.
	Written []string
	// Skipped explains why nothing was written when that is expected rather
	// than a failure (the engine is disabled, or the directory is read-only
	// because the checkout already holds the scripts).
	Skipped string
}

// engineEnsureMu serialises the writes and memoizes a successful run per
// directory, so a repeated call inside one panel process does no disk work.
var (
	engineEnsureMu   sync.Mutex
	engineEnsuredFor = map[string]string{}
)

// EngineDir is the directory the engine's Python sources live in: the directory
// of the script the supervisor starts.
func (c Config) EngineDir() string {
	dir := filepath.Dir(strings.TrimSpace(c.ScriptPath))
	if dir == "" || dir == "." {
		return filepath.Join(DefaultInstallDir, "vpngate")
	}
	return dir
}

// EnsureEngine writes this binary's embedded engine sources into the engine
// directory when they are missing or stale, and points ScriptPath at them when
// the configured script does not exist yet.
//
// A read-only directory is reported, not treated as a failure: a checkout or a
// container image that already ships the scripts must keep working, and the
// caller still runs CheckPrerequisites to find out what is actually there.
func (c *Config) EnsureEngine() EngineReport {
	report := EngineReport{Version: engineassets.Version()}
	if !c.Enabled {
		report.Skipped = "the VPNGate engine is disabled"
		return report
	}
	dir := c.EngineDir()
	report.Dir = dir

	changed, written, err := ensureEngineAssets(dir, report.Version)
	report.Changed = changed
	report.Written = written
	switch {
	case err != nil:
		if errors.Is(err, os.ErrPermission) {
			report.Skipped = "the engine directory is not writable: " + err.Error()
		} else {
			// Not fatal for the panel: the engine may already be in place.
			report.Skipped = "could not refresh the engine scripts: " + err.Error()
		}
	case changed:
		logger.Infof("vpngate: wrote the embedded engine (%s) to %s (%s)", report.Version, dir, strings.Join(written, ", "))
	}

	// A host without vpngate/ next to the binary resolves ScriptPath to the
	// install location that was missing. Now that it exists, use it.
	if _, statErr := os.Stat(c.ScriptPath); statErr != nil {
		candidate := filepath.Join(dir, managerScript)
		if info, candErr := os.Stat(candidate); candErr == nil && !info.IsDir() {
			c.ScriptPath = candidate
			report.Dir = dir
		}
	}
	return report
}

// ensureEngineAssets writes the embedded sources into dir unless the stamp says
// they are already there and complete. Returns whether anything was written.
func ensureEngineAssets(dir, version string) (bool, []string, error) {
	engineEnsureMu.Lock()
	defer engineEnsureMu.Unlock()
	if engineEnsuredFor[dir] == version {
		return false, nil, nil
	}

	if err := os.MkdirAll(dir, engineDirMode); err != nil {
		return false, nil, err
	}
	if engineAssetsCurrent(dir, version) {
		engineEnsuredFor[dir] = version
		return false, nil, nil
	}

	var written []string
	for _, name := range engineassets.Names {
		data, err := engineassets.Read(name)
		if err != nil {
			return false, written, fmt.Errorf("read embedded %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		// Identical content keeps its old timestamp: only a real difference
		// touches the disk, so a rebuild that did not change the engine costs
		// nothing.
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, data) {
			continue
		}
		if err := writeFileAtomic(path, data, engineFileMode); err != nil {
			return len(written) > 0, written, err
		}
		written = append(written, name)
	}
	// The stamp goes last: a half-written set must never look current.
	if err := writeFileAtomic(filepath.Join(dir, engineVersionFile), []byte(version+"\n"), engineFileMode); err != nil {
		return len(written) > 0, written, err
	}
	engineEnsuredFor[dir] = version
	return len(written) > 0, written, nil
}

// engineAssetsCurrent reports whether dir holds the stamped build and every
// script it names at the expected size. A file that was deleted, truncated or
// edited by hand fails the size check and is written again: the engine directory
// belongs to the panel, and the scripts in it must be the ones this binary was
// built with.
func engineAssetsCurrent(dir, version string) bool {
	stamp, err := os.ReadFile(filepath.Join(dir, engineVersionFile))
	if err != nil || strings.TrimSpace(string(stamp)) != version {
		return false
	}
	for _, name := range engineassets.Names {
		data, readErr := engineassets.Read(name)
		if readErr != nil {
			return false
		}
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr != nil || info.IsDir() || info.Size() != int64(len(data)) {
			return false
		}
	}
	return true
}

// writeFileAtomic replaces path in one rename, so a panel that dies mid-write
// never leaves a truncated script for the next boot to execute.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// resetEngineEnsureCache forgets the memoized writes; tests need it because the
// engine directories they use are recreated at the same paths.
func resetEngineEnsureCache() {
	engineEnsureMu.Lock()
	engineEnsuredFor = map[string]string{}
	engineEnsureMu.Unlock()
}
