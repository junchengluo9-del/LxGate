package vpngate

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeEngine is a minimal stand-in for vpngate_manager.py: it serves the health
// and nodes endpoints the supervisor probes, so the lifecycle can be tested
// without OpenVPN, tun devices, or root.
const fakeEngine = `import http.server, json, os

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/health":
            body = b'{"ok": true, "embedded": true}'
        elif self.path == "/api/nodes":
            body = b'{"nodes": [], "state": {"active_openvpn_node_id": ""}}'
        else:
            body = b'{"error": "not found"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass

port = int(os.environ.get("UI_PORT", "0"))
print("fake engine ready on", port, flush=True)
http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
`

func freePort(t *testing.T) int {
	t.Helper()
	// The supervisor's client derives its URL from the config, so the fixture
	// engine must listen on the configured port. Bind a port, note it, release it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func writeFakeEngine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "vpngate_manager.py")
	if err := os.WriteFile(path, []byte(fakeEngine), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}

func TestSupervisorDisabledDoesNotStart(t *testing.T) {
	supervisor := NewSupervisor(Config{Enabled: false})
	supervisor.Start(context.Background())
	defer supervisor.Stop()

	status := supervisor.Status()
	if status.State != StateDisabled {
		t.Fatalf("state = %q, want %q", status.State, StateDisabled)
	}
	if status.Available || status.Running {
		t.Fatalf("a disabled engine must not be available/running: %#v", status)
	}
}

func TestSupervisorMissingPythonIsUnavailable(t *testing.T) {
	script := writeFakeEngine(t)
	supervisor := NewSupervisor(Config{
		Enabled:      true,
		PythonBin:    filepath.Join(t.TempDir(), "definitely-not-python"),
		ScriptPath:   script,
		DataDir:      t.TempDir(),
		APIPort:      18787,
		ProxyPort:    7928,
		Token:        "0123456789abcdef",
		StartTimeout: time.Second,
	})
	supervisor.Start(context.Background())
	defer supervisor.Stop()

	status := supervisor.Status()
	if status.State != StateUnavailable {
		t.Fatalf("state = %q, want %q (%s)", status.State, StateUnavailable, status.Reason)
	}
	if status.Reason == "" {
		t.Fatal("an unavailable engine must explain why")
	}
	if status.Running {
		t.Fatal("nothing is running")
	}
}

func TestSupervisorRunsAndStops(t *testing.T) {
	requirePython(t)
	script := writeFakeEngine(t)
	port := freePort(t)
	dataDir := t.TempDir()
	supervisor := NewSupervisor(Config{
		Enabled:      true,
		PythonBin:    "python3",
		ScriptPath:   script,
		DataDir:      dataDir,
		APIPort:      port,
		ProxyPort:    7928,
		Token:        "0123456789abcdef",
		APITimeout:   2 * time.Second,
		StartTimeout: 15 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor.Start(ctx)
	defer supervisor.Stop()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if status := supervisor.Status(); status.Running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	status := supervisor.Status()
	if !status.Running {
		t.Fatalf("engine did not reach running: state=%q reason=%q tail=%v", status.State, status.Reason, supervisor.Tail())
	}
	if status.PID <= 0 {
		t.Fatal("running engine must report a pid")
	}
	if got, want := status.SocksURL, "socks5://127.0.0.1:7928"; got != want {
		t.Fatalf("socks url = %q, want %q", got, want)
	}
	if status.APIAddress != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatalf("api address = %q", status.APIAddress)
	}

	// The engine answers through the token-bearing client.
	if _, err := supervisor.Client().Nodes(context.Background()); err != nil {
		t.Fatalf("nodes through supervisor client: %v", err)
	}

	cancel()
	supervisor.Stop()
	stopped := supervisor.Status()
	if stopped.Running {
		t.Fatalf("engine still running after Stop: %#v", stopped)
	}
}
