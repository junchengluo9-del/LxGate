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
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"

	"github.com/gin-gonic/gin"
)

// DockerUpdateCommand is the documented way to move a containerised LxGate to
// the newest image. A container never rewrites its own image, so the panel shows
// this instead of pretending it can self-update.
const DockerUpdateCommand = "docker compose pull && docker compose up -d"

// SystemController serves the in-app version check and the update trigger.
//
// Division of labour: GET /panel/api/system/version reads GitHub Releases for
// junchengluo9-del/LxGate, POST /panel/api/system/update either hands the
// container case back to the operator (docker compose) or starts the existing
// native updater, which replaces the binary and restarts the service while
// keeping the SQLite database and vpngate_data/ untouched.
type SystemController struct {
	BaseController

	panelService panel.PanelService
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
}

func (a *SystemController) version(c *gin.Context) {
	payload := a.collectVersion(c.Request.Context())
	jsonObj(c, payload, nil)
}

func (a *SystemController) collectVersion(ctx context.Context) versionPayload {
	repo := config.GetReleaseRepo()
	docker := a.dockerProbe != nil && a.dockerProbe()
	current := config.GetPanelVersion()

	payload := versionPayload{
		CurrentVersion: current,
		Channel:        "stable",
		Repo:           repo,
		IsDocker:       docker,
		CanSelfUpdate:  !docker,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}
	if docker {
		payload.UpdateCommand = DockerUpdateCommand
	}

	// The GitHub call is capped so a blocked network answers the panel quickly.
	checkCtx, cancel := context.WithTimeout(ctx, release.DefaultTimeout)
	defer cancel()
	info, err := a.releaseClient.Latest(checkCtx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			payload.CheckError = releaseCheckMessage(err)
			logger.Debug("system: release check failed:", err)
		}
		return payload
	}

	payload.LatestVersion = release.NormalizeTag(info.TagName)
	payload.UpdateAvailable = release.IsNewer(info.TagName, current)
	payload.Changelog = info.Body
	payload.ReleaseName = info.Name
	payload.ReleaseURL = info.HTMLURL
	if !info.PublishedAt.IsZero() {
		payload.PublishedAt = info.PublishedAt.Format(time.RFC3339)
	}
	return payload
}

func releaseCheckMessage(err error) string {
	if errors.Is(err, release.ErrNoRelease) {
		return "no published release yet"
	}
	return "could not reach GitHub releases: " + err.Error()
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
	jsonObj(c, gin.H{
		"started":      true,
		"mode":         "native",
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
	jsonObj(c, gin.H{
		"name":           config.GetDisplayName(),
		"version":        config.GetPanelVersion(),
		"baseVersion":    config.GetBaseVersion(),
		"buildCommit":    config.GetBuildCommit(),
		"buildDate":      config.GetBuildDate(),
		"devBuild":       config.IsDevBuild(),
		"isDocker":       docker,
		"repo":           config.GetReleaseRepo(),
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
