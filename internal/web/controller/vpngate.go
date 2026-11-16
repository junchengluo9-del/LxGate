package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/vpngate"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/gin-gonic/gin"
)

// VPNGateController exposes the embedded VPNGate engine to the panel.
//
// Every route lives under /panel/api/vpngate and therefore inherits the panel's
// session/2FA/CSRF middleware: the engine itself only listens on 127.0.0.1 and
// authenticates the Go backend with a shared token, so no engine endpoint is
// ever reachable from the internet.
type VPNGateController struct {
	BaseController

	settingService    service.SettingService
	xraySetting       service.XraySettingService
	xrayService       service.XrayService
	engineCallTimeout time.Duration
}

// NewVPNGateController creates the controller and registers its routes on the
// supplied group (already prefixed with /panel/api/vpngate).
func NewVPNGateController(g *gin.RouterGroup) *VPNGateController {
	a := &VPNGateController{engineCallTimeout: 25 * time.Second}
	a.initRouter(g)
	return a
}

func (a *VPNGateController) initRouter(g *gin.RouterGroup) {
	g.GET("/status", a.status)
	g.GET("/nodes", a.nodes)
	g.GET("/logs", a.logs)
	g.GET("/xray-binding", a.getBinding)

	g.POST("/refresh", a.refresh)
	g.POST("/check", a.check)
	g.POST("/test-nodes", a.testNodes)
	g.POST("/connect", a.connect)
	g.POST("/disconnect", a.disconnect)
	g.POST("/routing", a.setRouting)
	g.POST("/test-proxy", a.testProxy)
	g.POST("/xray-bind", a.bindOutbound)
	g.POST("/xray-unbind", a.unbindOutbound)
}

// status reports the supervisor's view of the engine plus, when it is up, the
// engine's own per-service health report. A stopped engine is a normal answer,
// not an error: the page must render on a host without OpenVPN.
func (a *VPNGateController) status(c *gin.Context) {
	supervisor := vpngate.GetSupervisor()
	payload := gin.H{"supervisor": supervisor.Status()}

	if supervisor.Status().Available {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if raw, err := supervisor.Client().EngineStatus(ctx); err == nil {
			if decoded, decErr := rawMessage(raw); decErr == nil {
				payload["engine"] = decoded
			}
		} else {
			payload["engineError"] = engineErrorMessage(err)
		}
	}
	payload["xray"] = a.bindingState()
	jsonObj(c, payload, nil)
}

func (a *VPNGateController) nodes(c *gin.Context) {
	a.forwardGet(c, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.Nodes(ctx)
	})
}

func (a *VPNGateController) logs(c *gin.Context) {
	a.forwardGet(c, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.Logs(ctx)
	})
}

func (a *VPNGateController) refresh(c *gin.Context) {
	a.forwardPost(c, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.RefreshNodes(ctx)
	})
}

func (a *VPNGateController) check(c *gin.Context) {
	a.forwardPost(c, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.CheckNodes(ctx)
	})
}

func (a *VPNGateController) testProxy(c *gin.Context) {
	a.forwardPost(c, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.TestProxy(ctx)
	})
}

func (a *VPNGateController) testNodes(c *gin.Context) {
	var form struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	if len(form.IDs) > 256 {
		jsonMsg(c, "too many nodes in one request", errors.New("select at most 256 nodes"))
		return
	}
	// A speed test runs openvpn per node; give it a longer budget than a read.
	timeout := 4 * time.Minute
	a.forwardPostWithin(c, timeout, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.TestNodes(ctx, form.IDs)
	})
}

func (a *VPNGateController) connect(c *gin.Context) {
	var form struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	id := strings.TrimSpace(form.ID)
	if id == "" {
		jsonMsg(c, "a node id is required", errors.New("missing node id"))
		return
	}
	a.forwardPostWithin(c, 2*time.Minute, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.Connect(ctx, id)
	})
}

func (a *VPNGateController) disconnect(c *gin.Context) {
	a.forwardPostWithin(c, 2*time.Minute, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.Disconnect(ctx)
	})
}

type routingForm struct {
	Mode        string `json:"mode"`
	Country     string `json:"country"`
	IPType      string `json:"ipType"`
	FavFallback *bool  `json:"favFallback"`
}

func (a *VPNGateController) setRouting(c *gin.Context) {
	var form routingForm
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(form.Mode))
	switch mode {
	case "auto", "fixed_region", "fixed_ip", "favorites":
	default:
		jsonMsg(c, "unsupported routing mode", errors.New("mode must be one of auto, fixed_region, fixed_ip, favorites"))
		return
	}
	ipType := strings.ToLower(strings.TrimSpace(form.IPType))
	switch ipType {
	case "":
		ipType = "all"
	case "all", "residential", "hosting":
	default:
		jsonMsg(c, "unsupported ip type", errors.New("ipType must be all, residential or hosting"))
		return
	}
	country := strings.ToUpper(strings.TrimSpace(form.Country))
	if mode == "fixed_region" && country == "" {
		jsonMsg(c, "a country code is required for fixed region mode", errors.New("missing country"))
		return
	}
	fallback := true
	if form.FavFallback != nil {
		fallback = *form.FavFallback
	}
	a.forwardPostWithin(c, 90*time.Second, func(client *vpngate.Client, ctx context.Context) (json.RawMessage, error) {
		return client.SetRouting(ctx, mode, country, ipType, fallback)
	})
}

// getBinding reports whether the Xray template currently carries the
// vpngate-out socks outbound.
func (a *VPNGateController) getBinding(c *gin.Context) {
	jsonObj(c, a.bindingState(), nil)
}

func (a *VPNGateController) bindingState() gin.H {
	supervisor := vpngate.GetSupervisor()
	cfg := supervisor.Config()
	state := gin.H{
		"tag":          vpngate.OutboundTag,
		"bound":        false,
		"proxyAddress": cfg.ProxyAddress(),
		"socksUrl":     "socks5://" + cfg.ProxyAddress(),
		"httpProxyUrl": "http://" + cfg.ProxyAddress(),
		"ruleExample":  vpngate.RoutingRuleSnippet(vpngate.OutboundTag),
	}
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		state["error"] = err.Error()
		return state
	}
	state["configured"] = strings.TrimSpace(template) != ""
	state["bound"] = vpngate.HasOutbound(template, vpngate.OutboundTag)
	return state
}

// bindOutbound inserts (or refreshes) the vpngate-out socks outbound and reloads
// Xray so the new outbound becomes selectable in routing. Client traffic that
// does not target the tag is unaffected.
func (a *VPNGateController) bindOutbound(c *gin.Context) {
	supervisor := vpngate.GetSupervisor()
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		jsonMsg(c, "failed to read the Xray template", err)
		return
	}
	if strings.TrimSpace(template) == "" {
		jsonMsg(c, "the Xray template is empty", errors.New("nothing to bind"))
		return
	}
	updated, changed, err := vpngate.EnsureOutbound(template, vpngate.DefaultOutboundSpec(supervisor.Config()))
	if err != nil {
		jsonMsg(c, "failed to bind the VPNGate outbound", err)
		return
	}
	if changed {
		if err := a.xraySetting.SaveXraySetting(updated); err != nil {
			jsonMsg(c, "failed to save the Xray template", err)
			return
		}
		if a.xrayService.IsXrayRunning() {
			if err := a.xrayService.RestartXray(false); err != nil {
				jsonMsg(c, "outbound saved but Xray did not reload", err)
				return
			}
		}
	}
	state := a.bindingState()
	state["changed"] = changed
	jsonObj(c, state, nil)
}

// unbindOutbound removes the outbound and any routing rule that pointed at it.
func (a *VPNGateController) unbindOutbound(c *gin.Context) {
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		jsonMsg(c, "failed to read the Xray template", err)
		return
	}
	updated, changed, err := vpngate.RemoveOutbound(template, vpngate.OutboundTag)
	if err != nil {
		jsonMsg(c, "failed to unbind the VPNGate outbound", err)
		return
	}
	if changed {
		if err := a.xraySetting.SaveXraySetting(updated); err != nil {
			jsonMsg(c, "failed to save the Xray template", err)
			return
		}
		if a.xrayService.IsXrayRunning() {
			if err := a.xrayService.RestartXray(false); err != nil {
				jsonMsg(c, "outbound removed but Xray did not reload", err)
				return
			}
		}
	}
	state := a.bindingState()
	state["changed"] = changed
	jsonObj(c, state, nil)
}

// forwardGet proxies one engine read and unwraps its JSON into the panel's
// standard envelope.
func (a *VPNGateController) forwardGet(c *gin.Context, call func(*vpngate.Client, context.Context) (json.RawMessage, error)) {
	a.forwardWithin(c, a.engineCallTimeout, call)
}

// forwardPost proxies one engine action.
func (a *VPNGateController) forwardPost(c *gin.Context, call func(*vpngate.Client, context.Context) (json.RawMessage, error)) {
	a.forwardWithin(c, a.engineCallTimeout, call)
}

// forwardPostWithin proxies an action with an explicit time budget.
func (a *VPNGateController) forwardPostWithin(c *gin.Context, timeout time.Duration, call func(*vpngate.Client, context.Context) (json.RawMessage, error)) {
	a.forwardWithin(c, timeout, call)
}

func (a *VPNGateController) forwardWithin(c *gin.Context, timeout time.Duration, call func(*vpngate.Client, context.Context) (json.RawMessage, error)) {
	supervisor := vpngate.GetSupervisor()
	status := supervisor.Status()
	if !status.Enabled {
		jsonMsg(c, "the VPNGate engine is disabled", errors.New(status.State))
		return
	}
	if !status.Available {
		reason := status.Reason
		if reason == "" {
			reason = "the VPNGate engine is not available on this host"
		}
		jsonMsg(c, "the VPNGate engine is not available", errors.New(reason))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	raw, err := call(supervisor.Client(), ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			jsonMsg(c, "the VPNGate engine did not answer in time", err)
			return
		}
		if errors.Is(err, context.Canceled) {
			c.AbortWithStatus(http.StatusRequestTimeout)
			return
		}
		jsonMsg(c, engineErrorMessage(err), err)
		return
	}
	decoded, err := rawMessage(raw)
	if err != nil {
		jsonMsg(c, "the VPNGate engine returned an unreadable response", err)
		return
	}
	jsonObj(c, decoded, nil)
}

func rawMessage(raw json.RawMessage) (any, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// engineErrorMessage turns a transport or API error into one short line the UI
// can show: the engine's own Chinese message when it sent one, otherwise the
// transport error without a stack of wrapping.
func engineErrorMessage(err error) string {
	var apiErr *vpngate.APIError
	if errors.As(err, &apiErr) {
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(apiErr.Body), &body) == nil {
			if body.Error != "" {
				return body.Error
			}
			if body.Message != "" {
				return body.Message
			}
		}
		return apiErr.Error()
	}
	if errors.Is(err, vpngate.ErrEngineUnavailable) {
		return "vpngate engine is not reachable yet; it may still be starting"
	}
	return err.Error()
}
