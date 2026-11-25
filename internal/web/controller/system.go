package controller

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/release"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"

	"github.com/gin-gonic/gin"
)

// DockerUpdateCommand is the documented way to move a containerised LxGate to
// the newest image. A container never rewrites its own image, so the panel shows
// this instead of pretending it can self-update.
const DockerUpdateCommand = "docker compose pull && docker compose up -d"

// SystemController serves the in-app version check, the update settings and the
// update trigger.
//
// Division of labour: GET /panel/api/system/version reads GitHub Releases for
// junchengluo9-del/LxGate, POST /panel/api/system/update either hands the
// container case back to the operator (docker compose) or starts the existing
// native updater, which replaces the binary and restarts the service while
// keeping the SQLite database and vpngate_data/ untouched.
//
// There is one update path, not two. The check asks for the newest published
// release and falls back to the rolling dev-latest pre-release when the
// repository has no stable release yet (see release.LatestAvailable), and the
// update installs whichever of the two the check resolved. LxGate's release
// repository is public, so neither the check nor the download needs a
// credential. The update-settings endpoints remain for older clients and keep
// answering about the channel and token stored by earlier versions, but the
// panel itself no longer needs either.
type SystemController struct {
	BaseController

	panelService   panel.PanelService
	settingService service.SettingService
	// releaseClient and the docker probe are seam fields so tests can point them
	// at a fixture server and force either deployment mode.
	releaseClient *release.Client
	dockerProbe   func() bool

	// checkMu guards the last GitHub answer. GitHub's anonymous limit is 60
	// calls an hour per address, shared with everything else on the host, so
	// one answer is reused while it is fresh instead of spending a call per
	// page load.
	checkMu    sync.Mutex
	lastAnswer releaseAnswer
	hasAnswer  bool
}

const (
	// releaseCheckTTL is how long one answer is reused. Five minutes keeps a
	// reloaded page or a second tab from costing a call, and is short enough
	// that a new release shows up on the next visit.
	releaseCheckTTL = 5 * time.Minute
	// releaseCheckDebounce floors a forced check (the page's "Check again"
	// button) so pressing it repeatedly reuses an answer younger than this
	// rather than spending a call per press.
	releaseCheckDebounce = 20 * time.Second
)

// releaseAnswer is one release check: what it found, which channel it came
// from, and when it was asked. The same value is what the cache holds.
type releaseAnswer struct {
	info    *release.Info
	channel string
	err     error
	at      time.Time
}

// NewSystemController creates the controller and registers its routes on a
// group already prefixed with /panel/api/system.
func NewSystemController(g *gin.RouterGroup) *SystemController {
	a := &SystemController{
		releaseClient: release.NewClient(config.GetReleaseRepo()),
		dockerProbe:   IsDockerEnvironment,
	}
	a.initRouter(g)
	return a
}

func (a *SystemController) initRouter(g *gin.RouterGroup) {
	g.GET("/version", a.version)
	g.POST("/update", a.update)
	g.GET("/update-settings", a.getUpdateSettings)
	g.POST("/update-settings", a.saveUpdateSettings)
	g.GET("/update-status", a.updateStatus)
	g.GET("/info", a.info)
}

// IsDockerEnvironment reports whether the panel runs inside a container.
func IsDockerEnvironment() bool {
	if v := strings.TrimSpace(os.Getenv("LXGATE_DOCKER")); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// versionPayload is what the UI's update badge and System page read.
type versionPayload struct {
	CurrentVersion  string `json:"currentVersion"`
	Channel         string `json:"channel"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Changelog       string `json:"changelog,omitempty"`
	ReleaseName     string `json:"releaseName,omitempty"`
	ReleaseURL      string `json:"releaseUrl,omitempty"`
	PublishedAt     string `json:"publishedAt,omitempty"`
	CheckedAt       string `json:"checkedAt"`
	Repo            string `json:"repo"`
	IsDocker        bool   `json:"isDocker"`
	CanSelfUpdate   bool   `json:"canSelfUpdate"`
	UpdateCommand   string `json:"updateCommand,omitempty"`
	CheckError      string `json:"checkError,omitempty"`
	// Prerelease tells the UI that the resolved release is a rolling
	// pre-release (the dev channel), so it can label it correctly.
	Prerelease bool `json:"prerelease,omitempty"`
	// LatestCommit is the release's commit on the dev channel, where the tag
	// (dev-latest) identifies nothing by itself.
	LatestCommit  string `json:"latestCommit,omitempty"`
	CurrentCommit string `json:"currentCommit,omitempty"`
}

func (a *SystemController) version(c *gin.Context) {
	payload := a.collectVersion(c.Request.Context(), wantsFreshCheck(c))
	jsonObj(c, payload, nil)
}

// wantsFreshCheck reports whether the caller asked to skip the cached answer.
// The System page's "Check again" button sends refresh=1; a plain page load
// does not, and reuses an answer that is a few minutes old.
func wantsFreshCheck(c *gin.Context) bool {
	switch strings.ToLower(strings.TrimSpace(c.Query("refresh"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// updateSettings is what GET and POST /update-settings exchange. The panel no
// longer renders it: the release repository is public and the check resolves
// one release by itself. The endpoint stays so scripts and older pages keep
// working, and so the channel and token an earlier version stored can still be
// read back instead of failing.
type updateSettings struct {
	Channel           string `json:"channel"`
	HasGithubToken    bool   `json:"hasGithubToken"`
	GithubTokenMasked string `json:"githubTokenMasked,omitempty"`
	// TokenFileInPlace reports whether the updater can read the stored token
	// from disk. The native updater runs update.sh as root and reads the file
	// the panel mirrors the token into.
	TokenFileInPlace bool `json:"tokenFileInPlace,omitempty"`
}

// settings fills the channel and token from the settings table, tolerating a
// panel whose database is not ready yet (an empty answer is stable/no token).
func (a *SystemController) settings() (channel string, token string) {
	channel = release.ChannelStable
	if value, err := a.settingService.GetUpdateChannel(); err == nil {
		channel = value
	}
	if value, err := a.settingService.GetGithubToken(); err == nil {
		token = value
	}
	if token == "" {
		// Fall back to the environment so an operator can supply the token from
		// the service unit instead of the database.
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
		if token == "" {
			token = strings.TrimSpace(os.Getenv("GITHUB_API_TOKEN"))
		}
	}
	return channel, token
}

func (a *SystemController) collectVersion(ctx context.Context, force bool) versionPayload {
	repo := config.GetReleaseRepo()
	docker := a.dockerProbe != nil && a.dockerProbe()
	current := config.GetPanelVersion()

	payload := versionPayload{
		CurrentVersion: current,
		CurrentCommit:  release.ShortCommit(config.GetBuildCommit()),
		Channel:        release.ChannelStable,
		Repo:           repo,
		IsDocker:       docker,
		CanSelfUpdate:  !docker,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}
	if docker {
		payload.UpdateCommand = DockerUpdateCommand
	}
	if a.releaseClient == nil {
		payload.CheckError = "the release client is not configured"
		return payload
	}

	answer := a.releaseForCheck(ctx, force)
	payload.CheckedAt = answer.at.Format(time.RFC3339)
	payload.Channel = answer.channel
	if answer.err != nil {
		if !errors.Is(answer.err, context.Canceled) {
			payload.CheckError = releaseCheckMessage(answer.err)
			logger.Debug("system: release check failed:", answer.err)
		}
		return payload
	}

	info := answer.info
	payload.Prerelease = info.Prerelease
	payload.Changelog = info.Body
	payload.ReleaseName = info.Name
	payload.ReleaseURL = info.HTMLURL
	if !info.PublishedAt.IsZero() {
		payload.PublishedAt = info.PublishedAt.Format(time.RFC3339)
	}

	if answer.channel == release.ChannelDev {
		// The dev tag is fixed (dev-latest), so the commit is what tells two
		// builds apart; the tag itself would always look "current".
		latestCommit := release.ExtractCommit(info)
		payload.LatestCommit = release.ShortCommit(latestCommit)
		payload.LatestVersion = release.DevTag
		if latestCommit != "" {
			payload.LatestVersion = release.ChannelDev + "+" + release.ShortCommit(latestCommit)
		}
		payload.UpdateAvailable = latestCommit == "" || !release.CommitsEqual(config.GetBuildCommit(), latestCommit)
		return payload
	}

	payload.LatestVersion = release.NormalizeTag(info.TagName)
	payload.UpdateAvailable = release.IsNewer(info.TagName, current)
	return payload
}

// releaseForCheck answers the release the panel should offer, reusing the last
// answer while it is fresh. force asks for a new call (the page's "Check
// again"), still floored by releaseCheckDebounce so a held-down button cannot
// spend GitHub's anonymous limit. A failure is cached like a success: a panel
// that cannot reach GitHub must not retry on every page load. A request the
// caller abandoned is not cached, because it says nothing about GitHub.
func (a *SystemController) releaseForCheck(ctx context.Context, force bool) releaseAnswer {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()

	if a.hasAnswer {
		age := time.Since(a.lastAnswer.at)
		if age < releaseCheckTTL && (!force || age < releaseCheckDebounce) {
			return a.lastAnswer
		}
	}

	// The GitHub call is capped so a blocked network answers the panel quickly.
	checkCtx, cancel := context.WithTimeout(ctx, release.DefaultTimeout)
	defer cancel()
	info, channel, err := a.releaseClient.LatestAvailable(checkCtx)
	answer := releaseAnswer{info: info, channel: channel, err: err, at: time.Now()}
	if ctx.Err() == nil {
		a.lastAnswer = answer
		a.hasAnswer = true
	}
	return answer
}

// releaseCheckMessage turns a failed release check into the line the System
// page shows. The three expected failures say what to do about them in their
// own message; anything else is unexplained and gets the generic prefix.
func releaseCheckMessage(err error) string {
	switch {
	case errors.Is(err, release.ErrRateLimited),
		errors.Is(err, release.ErrTokenRejected),
		errors.Is(err, release.ErrNoRelease):
		return err.Error()
	}
	return "could not reach GitHub releases: " + err.Error()
}

func (a *SystemController) getUpdateSettings(c *gin.Context) {
	channel, token := a.settings()
	settings := updateSettings{
		Channel:        channel,
		HasGithubToken: token != "",
	}
	if token != "" {
		settings.GithubTokenMasked = service.MaskGithubToken(token)
	}
	if info, err := os.Stat(config.GetGithubTokenFilePath()); err == nil && info.Mode().Perm()&0o077 == 0 {
		settings.TokenFileInPlace = true
	}
	jsonObj(c, settings, nil)
}

// updateSettingsForm is the POST body. An empty githubToken means "keep the
// stored one" — the GET never returns the value, so the form cannot round-trip
// it; clearing needs its own flag.
type updateSettingsForm struct {
	Channel          string `json:"channel" form:"channel" validate:"omitempty,oneof=stable dev"`
	GithubToken      string `json:"githubToken" form:"githubToken" validate:"omitempty,max=255"`
	ClearGithubToken bool   `json:"clearGithubToken" form:"clearGithubToken"`
}

func (a *SystemController) saveUpdateSettings(c *gin.Context) {
	form, ok := middleware.BindAndValidate[updateSettingsForm](c)
	if !ok {
		return
	}
	if strings.TrimSpace(form.Channel) != "" {
		if err := a.settingService.SetUpdateChannel(form.Channel); err != nil {
			jsonMsg(c, "failed to save the update channel", err)
			return
		}
	}
	token := strings.TrimSpace(form.GithubToken)
	switch {
	case form.ClearGithubToken:
		if err := a.settingService.SetGithubToken(""); err != nil {
			jsonMsg(c, "failed to clear the GitHub token", err)
			return
		}
	case token != "":
		if err := a.settingService.SetGithubToken(token); err != nil {
			jsonMsg(c, "failed to save the GitHub token", err)
			return
		}
	}
	a.getUpdateSettings(c)
}

// update starts a native self-update, or explains the container workflow.
func (a *SystemController) update(c *gin.Context) {
	if a.dockerProbe != nil && a.dockerProbe() {
		jsonObj(c, gin.H{
			"started":      false,
			"mode":         "docker",
			"command":      DockerUpdateCommand,
			"message":      "This panel runs in Docker. Pull the new image on the host to update; the database and vpngate_data stay in their volumes.",
			"preserveData": []string{"/etc/lx-gate", "/opt/lxgate/vpngate_data"},
		}, nil)
		return
	}

	runID, err := a.startPanelUpdate(c.Request.Context())
	if err != nil {
		jsonMsg(c, "failed to start the update", err)
		return
	}
	jsonObj(c, gin.H{
		"started":      true,
		"mode":         "native",
		"runId":        strconv.FormatInt(runID, 10),
		"message":      "Update started. The panel restarts when the new binary is in place; the database and vpngate_data are preserved.",
		"preserveData": []string{config.GetDBPath(), "/opt/lxgate/vpngate_data"},
	}, nil)
}

// startPanelUpdate installs the release the page offered. The check resolves
// either the newest stable release or, when there is none, the rolling
// dev-latest build, and the updater has to follow the same one or the page
// would offer a version that the update does not install. When the check cannot
// answer, the stored channel decides as it always has.
func (a *SystemController) startPanelUpdate(ctx context.Context) (int64, error) {
	if a.releaseClient != nil {
		answer := a.releaseForCheck(ctx, false)
		if answer.err == nil && answer.info != nil {
			return a.panelService.StartUpdateChannel(answer.channel == release.ChannelDev)
		}
	}
	return a.panelService.StartUpdate()
}

func (a *SystemController) updateStatus(c *gin.Context) {
	jsonObj(c, a.panelService.GetUpdateStatus(), nil)
}

// info is the small static block the System page renders (paths, ports, mode)
// so operators do not have to guess where LxGate keeps its state.
func (a *SystemController) info(c *gin.Context) {
	docker := a.dockerProbe != nil && a.dockerProbe()
	channel, _ := a.settings()
	jsonObj(c, gin.H{
		"name":           config.GetDisplayName(),
		"version":        config.GetPanelVersion(),
		"baseVersion":    config.GetBaseVersion(),
		"buildCommit":    config.GetBuildCommit(),
		"buildDate":      config.GetBuildDate(),
		"devBuild":       config.IsDevBuild(),
		"isDocker":       docker,
		"repo":           config.GetReleaseRepo(),
		"channel":        channel,
		"dbPath":         config.GetDBPath(),
		"vpngateDataDir": "/opt/lxgate/vpngate_data",
		"updateCommand":  updateCommandFor(docker),
	}, nil)
}

func updateCommandFor(docker bool) string {
	if docker {
		return DockerUpdateCommand
	}
	return "lxgate update"
}
