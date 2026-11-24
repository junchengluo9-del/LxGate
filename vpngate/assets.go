// Package engineassets embeds the Python VPNGate engine that ships with LxGate.
//
// The engine is a sidecar: vpngate_manager.py owns the OpenVPN tunnels and the
// loopback proxies, and proxy_server.py / vpn_utils.py are the helpers it
// imports. They live in this directory next to the Go file only so the
// //go:embed directive below can reach them — Go cannot embed a parent
// directory. internal/vpngate reads them from here and writes them into the
// engine directory (/opt/lxgate/vpngate) whenever that copy is missing or
// belongs to a different build, so a panel installed from a tarball without
// vpngate/, an update that only replaced the binary, or an unreachable
// raw.githubusercontent.com can no longer leave the VPNGate page without an
// engine.
//
// Only the three scripts the sidecar executes are embedded. The tests beside
// them and any __pycache__ directory are not part of the shipped engine.
package engineassets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
)

//go:embed vpngate_manager.py proxy_server.py vpn_utils.py
var Files embed.FS

// Names lists every embedded engine file, in the order the panel writes them.
// The sidecar's own tests are deliberately absent: they are not needed at
// runtime and would only widen what a panel install writes to disk.
var Names = []string{
	"vpngate_manager.py",
	"proxy_server.py",
	"vpn_utils.py",
}

// Read returns one embedded engine file.
func Read(name string) ([]byte, error) {
	return fs.ReadFile(Files, name)
}

// Version identifies this exact set of engine sources. It changes whenever any
// of them changes, which is what lets the panel keep the copy on disk in step
// with the binary the sources were built into.
func Version() string {
	sum := sha256.New()
	for _, name := range Names {
		data, err := Read(name)
		if err != nil {
			// Embedded content is compiled in and cannot disappear at runtime;
			// hashing what is present keeps this helper total instead of
			// failing in the panel's boot path.
			continue
		}
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(data)
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}
