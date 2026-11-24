package controller

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
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
// junchengluo9-del/LxGate on the configured channel, POST
// /panel/api/system/update-settings stores that channel and the optional
// read-only token, and POST /panel/api/system/update either hands the container
// case back to the operator (docker compose) or starts the existing native
// updater, which replaces the binary and restarts the service while keeping the
// SQLite database and vpngate_data/ untouched.
//
// LxGate's release repository is public, so the version check and the update
// download work with no credential at all. A read-only token the operator saved
// in the update settings (or GITHUB_TOKEN in the panel's environment) is
// optional: it raises GitHub's anonymous API limit and lets the panel follow a
// private fork. A token never leaves the panel: the settings endpoint answers
// with hasGithubToken and a masked hint.
type SystemController struct {
	BaseController

	panelService   panel.PanelService
	settingService service.SettingService
	// releaseClient and the docker probe are seam fields so tests can point them
	// at a fixture server and force either deployment mode.
	releaseClient *release.Client
	dockerProbe   func() bool
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
	payload := a.collectVersion(c.Request.Context())
	jsonObj(c, payload, nil)
}

// updateSettings is what GET and POST /update-settings exchange.
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

func (a *SystemController) collectVersion(ctx context.Context) versionPayload {
	repo := config.GetReleaseRepo()
	docker := a.dockerProbe != nil && a.dockerProbe()
	current := config.GetPanelVersion()
	channel, token := a.settings()

	payload := versionPayload{
		CurrentVersion: current,
		CurrentCommit:  release.ShortCommit(config.GetBuildCommit()),
		Channel:        channel,
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

	// The GitHub call is capped so a blocked network answers the panel quickly.
	checkCtx, cancel := context.WithTimeout(ctx, release.DefaultTimeout)
	defer cancel()
	info, err := a.releaseClient.WithToken(token).Release(checkCtx, channel)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			payload.CheckError = releaseCheckMessage(err)
			logger.Debug("system: release check failed:", err)
		}
		return payload
	}

	payload.Prerelease = info.Prerelease
	payload.Changelog = info.Body
	payload.ReleaseName = info.Name
	payload.ReleaseURL = info.HTMLURL
	if !info.PublishedAt.IsZero() {
		payload.PublishedAt = info.PublishedAt.Format(time.RFC3339)
	}

	if channel == release.ChannelDev {
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

	runID, err := a.panelService.StartUpdate()
	if err != nil {
		jsonMsg(c, "failed to start the update", err)
		return
	}
	channel, _ := a.settings()
	jsonObj(c, gin.H{
		"started":      true,
		"mode":         "native",
		"channel":      channel,
		"runId":        strconv.FormatInt(runID, 10),
		"message":      "Update started. The panel restarts when the new binary is in place; the database and vpngate_data are preserved.",
		"preserveData": []string{config.GetDBPath(), "/opt/lxgate/vpngate_data"},
	}, nil)
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
