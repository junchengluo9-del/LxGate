package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			Started  bool     `json:"started"`
			Mode     string   `json:"mode"`
			Command  string   `json:"command"`
			Preserve []string `json:"preserveData"`
			Message  string   `json:"message"`
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

// newSystemDBTestRouter is newSystemTestRouter plus a real settings database, so
// the channel and token the controller reads come from the settings table.
func newSystemDBTestRouter(t *testing.T, handler http.HandlerFunc) (*gin.Engine, *SystemController) {
	t.Helper()
	newHostTestDB(t)
	return newSystemTestRouter(t, handler, false)
}

type updateSettingsResponse struct {
	Success bool `json:"success"`
	Obj     struct {
		Channel           string `json:"channel"`
		HasGithubToken    bool   `json:"hasGithubToken"`
		GithubTokenMasked string `json:"githubTokenMasked"`
		TokenFileInPlace  bool   `json:"tokenFileInPlace"`
	} `json:"obj"`
}

func doSystemJSON(t *testing.T, router *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// A private repository answers 404 without a token, and the panel has to say so
// instead of "no release published yet".
// A 404 without a token on the public repository means there is no release to
// install yet; the panel reports that and does not turn it into a demand for a
// token.
func TestSystemVersionExplainsMissingReleaseWithoutToken(t *testing.T) {
	router, _ := newSystemDBTestRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")

	rec := doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	var payload versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload.Obj.CheckError, "no published release") {
		t.Fatalf("checkError must name the missing release: %q", payload.Obj.CheckError)
	}
	if strings.Contains(payload.Obj.CheckError, "is private") {
		t.Fatalf("the public repository must not be reported as private: %q", payload.Obj.CheckError)
	}
}

// The anonymous API limit is reported as a limit, with the token as something
// that would raise it rather than a requirement for the check to work.
func TestSystemVersionReportsRateLimitWithoutToken(t *testing.T) {
	router, _ := newSystemDBTestRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")

	rec := doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	var payload versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload.Obj.CheckError, "anonymous limit") {
		t.Fatalf("checkError must explain the limit: %q", payload.Obj.CheckError)
	}
}

// A repository with no stable release still offers an update: the check falls
// back to the rolling dev-latest pre-release, whose commit decides whether an
// update is available. No setting has to be switched for this.
func TestSystemVersionFallsBackToDevLatest(t *testing.T) {
	var paths []string
	router, _ := newSystemTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "dev-latest",
			"name":         "Dev build",
			"body":         "commit=abcdef1234567890",
			"prerelease":   true,
			"published_at": "2026-11-17T03:12:11Z",
		})
	}, false)

	rec := doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	var payload struct {
		Success bool `json:"success"`
		Obj     struct {
			Channel         string `json:"channel"`
			LatestVersion   string `json:"latestVersion"`
			LatestCommit    string `json:"latestCommit"`
			UpdateAvailable bool   `json:"updateAvailable"`
			Prerelease      bool   `json:"prerelease"`
			CheckError      string `json:"checkError"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Obj.CheckError != "" {
		t.Fatalf("a dev-latest fallback is not an error: %q", payload.Obj.CheckError)
	}
	if payload.Obj.Channel != "dev" || !payload.Obj.Prerelease {
		t.Fatalf("obj = %#v", payload.Obj)
	}
	if payload.Obj.LatestCommit != "abcdef12" || payload.Obj.LatestVersion != "dev+abcdef12" {
		t.Fatalf("obj = %#v", payload.Obj)
	}
	if !payload.Obj.UpdateAvailable {
		t.Fatal("a build without a matching commit must see the dev release as an update")
	}
	want := []string{
		"/repos/junchengluo9-del/LxGate/releases/latest",
		"/repos/junchengluo9-del/LxGate/releases/tags/dev-latest",
	}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

// One answer serves every page load for five minutes, a forced check inside the
// debounce reuses it too, and a forced check after the debounce asks GitHub
// again -- that is what keeps a reloaded page from spending the anonymous
// 60-calls-an-hour limit.
func TestSystemVersionReusesRecentAnswer(t *testing.T) {
	var calls int
	router, controller := newSystemTestRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v9.9.9",
			"name":         "LxGate 9.9.9",
			"published_at": "2026-11-10T08:00:00Z",
		})
	}, false)

	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	if calls != 1 {
		t.Fatalf("a plain reload must reuse the answer, calls = %d", calls)
	}
	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version?refresh=1", "")
	if calls != 1 {
		t.Fatalf("a forced check inside the debounce must reuse the answer, calls = %d", calls)
	}

	// Past the debounce but inside the TTL: the button does reach GitHub.
	controller.checkMu.Lock()
	controller.lastAnswer.at = time.Now().Add(-time.Minute)
	controller.checkMu.Unlock()
	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version?refresh=1", "")
	if calls != 2 {
		t.Fatalf("a forced check must refetch, calls = %d", calls)
	}

	// And a page load straight after still reuses that new answer.
	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	// Past the TTL the next page load checks again by itself.
	controller.checkMu.Lock()
	controller.lastAnswer.at = time.Now().Add(-releaseCheckTTL - time.Minute)
	controller.checkMu.Unlock()
	doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	if calls != 3 {
		t.Fatalf("a stale answer must be refreshed, calls = %d", calls)
	}
}

// The settings endpoint stores the channel and the token, masks the token in its
// answer, and clears it on request. It is kept for older clients: the version
// check no longer needs, or sends, a token on a public repository.
func TestSystemUpdateSettingsRoundTrip(t *testing.T) {
	var gotAuth string
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")
	router, controller := newSystemDBTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"name":     "LxGate 9.9.9",
		})
	})

	rec := doSystemJSON(t, router, http.MethodPost, "/panel/api/system/update-settings",
		`{"channel":"dev","githubToken":"ghp_secret_value_1234"}`)
	var saved updateSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if !saved.Success {
		t.Fatalf("save failed: %s", rec.Body.String())
	}
	if saved.Obj.Channel != "dev" {
		t.Fatalf("channel = %q", saved.Obj.Channel)
	}
	if !saved.Obj.HasGithubToken {
		t.Fatal("hasGithubToken must be true after saving a token")
	}
	if strings.Contains(rec.Body.String(), "ghp_secret_value_1234") {
		t.Fatal("the token must never be returned by the API")
	}
	if saved.Obj.GithubTokenMasked == "" || saved.Obj.GithubTokenMasked == "ghp_secret_value_1234" {
		t.Fatalf("masked = %q", saved.Obj.GithubTokenMasked)
	}
	if !saved.Obj.TokenFileInPlace {
		t.Fatal("the updater's token file must exist for the native updater")
	}

	// A check no longer authenticates: the public repository needs no token,
	// and a stale token stored earlier must not break the check.
	rec = doSystemJSON(t, router, http.MethodGet, "/panel/api/system/version", "")
	if gotAuth != "" {
		t.Fatalf("the version check must be anonymous, Authorization = %q", gotAuth)
	}

	// An empty token in a later save keeps the stored one (the form never sees
	// the value), and clearing needs its own flag.
	doSystemJSON(t, router, http.MethodPost, "/panel/api/system/update-settings", `{"channel":"stable"}`)
	if !controller.settingService.HasGithubToken() {
		t.Fatal("an empty token must not wipe the stored one")
	}

	rec = doSystemJSON(t, router, http.MethodPost, "/panel/api/system/update-settings", `{"clearGithubToken":true}`)
	var cleared updateSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cleared.Obj.HasGithubToken {
		t.Fatal("clearGithubToken must remove the token")
	}
	if controller.settingService.HasGithubToken() {
		t.Fatal("the stored token must be gone")
	}
}
