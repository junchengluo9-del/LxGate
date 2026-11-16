package panel

import (
	"errors"
	"testing"
)

func TestSelfUpdateRefusesPlaceholderRepo(t *testing.T) {
	t.Setenv("LXGATE_REPO", "<ORG>/LxGate") // lxgate:keep
	if _, err := fetchPanelRelease(""); !errors.Is(err, errReleaseRepoNotConfigured) {
		t.Fatalf("fetchPanelRelease err = %v, want errReleaseRepoNotConfigured", err)
	}
	if _, err := downloadPanelUpdater(); !errors.Is(err, errReleaseRepoNotConfigured) {
		t.Fatalf("downloadPanelUpdater err = %v, want errReleaseRepoNotConfigured", err)
	}
}

func TestPanelUpdaterURLFollowsRepo(t *testing.T) {
	t.Setenv("LXGATE_REPO", "acme/LxGate")
	if got, want := panelUpdaterURL(), "https://raw.githubusercontent.com/acme/LxGate/main/update.sh"; got != want {
		t.Fatalf("panelUpdaterURL() = %q, want %q", got, want)
	}
}
