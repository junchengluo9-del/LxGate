package config

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readRebrandEnv parses tools/rebrand.env (KEY="value" lines).
func readRebrandEnv(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "tools", "rebrand.env"))
	if err != nil {
		t.Fatalf("open rebrand.env: %v", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

// The embedded identity must be what tools/rebrand.env declares: one place to
// change the ORG placeholder, the display name and the identifier.
func TestEmbeddedBrandMatchesRebrandEnv(t *testing.T) {
	env := readRebrandEnv(t)
	if got := GetName(); got != env["LXGATE_NAME"] {
		t.Errorf("GetName() = %q, rebrand.env LXGATE_NAME = %q", got, env["LXGATE_NAME"])
	}
	if got := strings.TrimSpace(repo); got != env["LXGATE_REPO"] {
		t.Errorf("embedded repo = %q, rebrand.env LXGATE_REPO = %q", got, env["LXGATE_REPO"])
	}
	if got := strings.TrimSpace(display); got != env["LXGATE_DISPLAY"] {
		t.Errorf("embedded display = %q, rebrand.env LXGATE_DISPLAY = %q", got, env["LXGATE_DISPLAY"])
	}
	if got := GetDisplayName(); got != env["LXGATE_DISPLAY"] {
		t.Errorf("GetDisplayName() = %q, rebrand.env LXGATE_DISPLAY = %q", got, env["LXGATE_DISPLAY"])
	}
	if strings.Contains(env["LXGATE_REPO"], "<ORG>") && releaseRepo == "" && os.Getenv("LXGATE_REPO") == "" && IsReleaseRepoConfigured() {
		t.Error("the <ORG> placeholder must never count as a configured release repo")
	}
}

// Runs the same guard as `make rebrand-check` / CI so `go test ./...` alone
// catches stray upstream download/update URLs and an out-of-date rebrand.
func TestRebrandGuardScript(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skip("not a git checkout")
	}
	for _, bin := range []string{"bash", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	cmd := exec.Command("bash", "tools/check-rebrand.sh")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tools/check-rebrand.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rebrand guard: OK") {
		t.Fatalf("unexpected guard output:\n%s", out)
	}
}
