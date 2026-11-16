package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/release"

	"github.com/gin-gonic/gin"
)

// newSystemTestRouter builds the /panel/api/system group with the release client
// pointed at a fixture server and the deployment mode forced by the caller.
func newSystemTestRouter(t *testing.T, handler http.HandlerFunc, docker bool) (*gin.Engine, *SystemController) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fixture := httptest.NewServer(handler)
	t.Cleanup(fixture.Close)

	router := gin.New()
	controller := NewSystemController(router.Group("/panel/api/system"))
	controller.releaseClient = release.NewClient("junchengluo9-del/LxGate")
	controller.releaseClient.SetBaseURL(fixture.URL)
	controller.dockerProbe = func() bool { return docker }
	return router, controller
}

func releaseFixture(tag, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		payload := map[string]any{
			"tag_name":     tag,
			"name":         "LxGate " + tag,
			"body":         body,
			"html_url":     "https://github.com/junchengluo9-del/LxGate/releases/tag/" + tag,
			"published_at": "2026-11-10T08:00:00Z",
		}
		_ = json.NewEncoder(w).Encode(payload)
	}
}

type versionResponse struct {
	Success bool `json:"success"`
	Obj     struct {
		CurrentVersion  string `json:"currentVersion"`
		LatestVersion   string `json:"latestVersion"`
		UpdateAvailable bool   `json:"updateAvailable"`
		Changelog       string `json:"changelog"`
		ReleaseURL      string `json:"releaseUrl"`
		Repo            string `json:"repo"`
		IsDocker        bool   `json:"isDocker"`
		CanSelfUpdate   bool   `json:"canSelfUpdate"`
		UpdateCommand   string `json:"updateCommand"`
		CheckError      string `json:"checkError"`
		CheckedAt       string `json:"checkedAt"`
	} `json:"obj"`
}

func TestSystemVersionReportsNewerRelease(t *testing.T) {
	router, _ := newSystemTestRouter(t, releaseFixture("v9.9.9", "## New\n- vpngate page"), false)

	req := httptest.NewRequest(http.MethodGet, "/panel/api/system/version", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if !payload.Success {
		t.Fatalf("not successful: %s", rec.Body.String())
	}
	if payload.Obj.CurrentVersion == "" {
		t.Fatal("current version must be reported")
	}
	if payload.Obj.LatestVersion != "9.9.9" {
		t.Fatalf("latest = %q", payload.Obj.LatestVersion)
	}
	if !payload.Obj.UpdateAvailable {
		t.Fatal("a 9.9.9 release must look newer than the embedded version")
	}
	if !strings.Contains(payload.Obj.Changelog, "vpngate page") {
		t.Fatalf("changelog missing: %q", payload.Obj.Changelog)
	}
	if payload.Obj.IsDocker {
		t.Fatal("native mode expected")
	}
	if !payload.Obj.CanSelfUpdate {
		t.Fatal("native installs can self-update")
	}
	if payload.Obj.UpdateCommand != "" {
		t.Fatalf("native mode should not advertise a docker command, got %q", payload.Obj.UpdateCommand)
	}
	if payload.Obj.Repo == "" {
		t.Fatal("repo must be reported so the UI can link the release feed")
	}
}

func TestSystemVersionDockerMode(t *testing.T) {
	router, _ := newSystemTestRouter(t, releaseFixture("v1.0.0", "body"), true)

	req := httptest.NewRequest(http.MethodGet, "/panel/api/system/version", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.Obj.IsDocker {
		t.Fatal("docker mode not detected")
	}
	if payload.Obj.CanSelfUpdate {
		t.Fatal("a container must not claim to self-update")
	}
	if payload.Obj.UpdateCommand != DockerUpdateCommand {
		t.Fatalf("update command = %q", payload.Obj.UpdateCommand)
	}
}

func TestSystemVersionSurvivesReleaseCheckFailure(t *testing.T) {
	router, _ := newSystemTestRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, false)

	req := httptest.NewRequest(http.MethodGet, "/panel/api/system/version", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A broken update feed must not break the page: it still knows its own version.
	if !payload.Success || payload.Obj.CurrentVersion == "" {
		t.Fatalf("payload = %s", rec.Body.String())
	}
	if payload.Obj.CheckError == "" {
		t.Fatal("the failed check must be reported so the UI can explain it")
	}
	if payload.Obj.CheckedAt == "" {
		t.Fatal("checkedAt must be set")
	}
}

func TestSystemUpdateInDockerReturnsCommand(t *testing.T) {
	router, _ := newSystemTestRouter(t, releaseFixture("v9.9.9", ""), true)

	req := httptest.NewRequest(http.MethodPost, "/panel/api/system/update", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload struct {
		Success bool `json:"success"`
		Obj     struct {
			Started   bool     `json:"started"`
			Mode      string   `json:"mode"`
			Command   string   `json:"command"`
			Preserve  []string `json:"preserveData"`
			Message   string   `json:"message"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if !payload.Success {
		t.Fatalf("docker guidance is a successful answer: %s", rec.Body.String())
	}
	if payload.Obj.Started {
		t.Fatal("a container cannot start a self-update")
	}
	if payload.Obj.Mode != "docker" || payload.Obj.Command != DockerUpdateCommand {
		t.Fatalf("obj = %#v", payload.Obj)
	}
	if len(payload.Obj.Preserve) == 0 {
		t.Fatal("docker guidance must name the volumes that keep user data")
	}
}

func TestSystemInfoBlock(t *testing.T) {
	t.Setenv("LXGATE_DOCKER", "1")
	router := gin.New()
	NewSystemController(router.Group("/panel/api/system"))

	req := httptest.NewRequest(http.MethodGet, "/panel/api/system/info", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var payload struct {
		Success bool `json:"success"`
		Obj     struct {
			Name          string `json:"name"`
			Version       string `json:"version"`
			IsDocker      bool   `json:"isDocker"`
			UpdateCommand string `json:"updateCommand"`
			DBPath        string `json:"dbPath"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.Success {
		t.Fatalf("payload = %s", rec.Body.String())
	}
	if !payload.Obj.IsDocker {
		t.Fatal("LXGATE_DOCKER=1 must be detected")
	}
	if payload.Obj.UpdateCommand != DockerUpdateCommand {
		t.Fatalf("command = %q", payload.Obj.UpdateCommand)
	}
	if payload.Obj.Version == "" || payload.Obj.Name == "" {
		t.Fatalf("identity missing: %#v", payload.Obj)
	}
}

func TestIsDockerEnvironmentHonoursEnv(t *testing.T) {
	t.Setenv("LXGATE_DOCKER", "true")
	if !IsDockerEnvironment() {
		t.Fatal("LXGATE_DOCKER=true must be detected")
	}
	t.Setenv("LXGATE_DOCKER", "0")
	// Without the env override the answer depends on /.dockerenv, which is
	// absent in this sandbox; the call must simply not panic.
	_ = IsDockerEnvironment()
}
