package vpngate

import "sync"

var (
	defaultSupervisorM sync.Mutex
	defaultSupervisor  *Supervisor
)

// GetSupervisor returns the process-wide engine supervisor, building it from the
// environment on first use. The panel starts it once at boot and stops it on
// shutdown; tests replace it with SetDefaultForTest.
func GetSupervisor() *Supervisor {
	defaultSupervisorM.Lock()
	defer defaultSupervisorM.Unlock()
	if defaultSupervisor == nil {
		defaultSupervisor = NewSupervisor(LoadConfig())
	}
	return defaultSupervisor
}

// SetDefaultForTest swaps the package supervisor and returns a restore func.
func SetDefaultForTest(s *Supervisor) func() {
	defaultSupervisorM.Lock()
	defer defaultSupervisorM.Unlock()
	previous := defaultSupervisor
	defaultSupervisor = s
	return func() {
		defaultSupervisorM.Lock()
		defer defaultSupervisorM.Unlock()
		defaultSupervisor = previous
	}
}
