package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/vpngate"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// Multi-exit endpoints.
//
// The engine is the source of truth for which exits exist and how they route;
// the Xray template is the source of truth for which inbound leaves through
// which exit. These handlers keep the two in step and never ask the operator to
// edit Xray JSON.

// exitBindForm is POST /panel/api/vpngate/exits/bind.
type exitBindForm struct {
	InboundTag string `json:"inboundTag"`
	// ExitID is the exit the inbound should leave through; empty means direct.
	ExitID string `json:"exitId"`
}

func (a *VPNGateController) registerExitRoutes(g *gin.RouterGroup) {
	g.GET("/exits", a.listExits)
	g.GET("/exits/bindings", a.listExits)
	g.POST("/exits", a.createExit)
	g.POST("/exits/create", a.createExit)
	g.POST("/exits/update", a.updateExit)
	g.POST("/exits/delete", a.deleteExit)
	g.POST("/exits/connect", a.connectExit)
	g.POST("/exits/auto-connect", a.autoConnectExit)
	g.POST("/exits/disconnect", a.disconnectExit)
	g.POST("/exits/bind", a.bindExit)
	g.POST("/exits/sync", a.syncExits)
}

// engineExits reads the engine's slot list. It is the one place that decides
// what the panel considers a configured exit.
func (a *VPNGateController) engineExits(ctx context.Context) ([]vpngate.ExitSpec, json.RawMessage, error) {
	supervisor := vpngate.GetSupervisor()
	status := supervisor.Status()
	if !status.Enabled || !status.Available {
		reason := status.Reason
		if reason == "" {
			reason = "the VPNGate engine is not available on this host"
		}
		return nil, nil, errors.New(reason)
	}
	raw, err := supervisor.Client().Exits(ctx)
	if err != nil {
		return nil, nil, err
	}
	exits, err := vpngate.ParseExits(raw)
	if err != nil {
		return nil, nil, err
	}
	return exits, raw, nil
}

// listExits returns the exit cards, the memory budget, the inbound options and
// the current per-inbound binding in one round-trip.
func (a *VPNGateController) listExits(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	exits, raw, engineErr := a.engineExits(ctx)

	payload := gin.H{
		"exits":  []any{},
		"memory": gin.H{},
	}
	if engineErr != nil {
		payload["engineError"] = engineErrorMessage(engineErr)
	} else {
		var decoded struct {
			Exits  []map[string]any `json:"exits"`
			Memory map[string]any   `json:"memory"`
		}
		if err := json.Unmarshal(raw, &decoded); err == nil {
			if decoded.Exits != nil {
				payload["exits"] = decoded.Exits
			}
			if decoded.Memory != nil {
				payload["memory"] = decoded.Memory
			}
		}
	}

	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		payload["xrayError"] = err.Error()
		template = ""
	}
	configured := strings.TrimSpace(template) != ""
	bindings := map[string]int{}
	if configured {
		bindings = vpngate.ReadExitBindings(template)
	}
	payload["bindings"] = bindings
	payload["registeredSlots"] = vpngate.RegisteredExitSlots(template)
	payload["templateConfigured"] = configured

	if user := session.GetLoginUser(c); user != nil {
		if inbounds, inErr := a.inboundService.GetInboundExitOptions(user.Id); inErr == nil {
			payload["inbounds"] = inbounds
		}
	}
	if exits != nil {
		payload["specs"] = vpngate.ExitSpecsFrom(exits)
	}
	jsonObj(c, payload, nil)
}

func (a *VPNGateController) createExit(c *gin.Context) {
	var form struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&form)
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	raw, err := vpngate.GetSupervisor().Client().CreateExit(ctx, strings.TrimSpace(form.Name))
	if err != nil {
		jsonMsg(c, "failed to create the exit", err)
		return
	}
	// Registering the new outbound right away means the operator can bind an
	// inbound to it in the same visit; a bind would do this anyway.
	if syncErr := a.syncExitOutbounds(ctx); syncErr != nil {
		jsonMsg(c, "exit created but its Xray outbound was not registered", syncErr)
		return
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

func (a *VPNGateController) updateExit(c *gin.Context) {
	var form map[string]any
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	exitID, _ := form["id"].(string)
	if strings.TrimSpace(exitID) == "" {
		jsonMsg(c, "an exit id is required", errors.New("missing exit id"))
		return
	}
	delete(form, "id")
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	raw, err := vpngate.GetSupervisor().Client().UpdateExit(ctx, exitID, form)
	if err != nil {
		jsonMsg(c, "failed to update the exit", err)
		return
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

func (a *VPNGateController) deleteExit(c *gin.Context) {
	var form struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	exitID := strings.TrimSpace(form.ID)
	if exitID == "" {
		jsonMsg(c, "an exit id is required", errors.New("missing exit id"))
		return
	}
	// Read the slot before deleting: the template cleanup needs it afterwards.
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	exits, _, err := a.engineExits(ctx)
	if err != nil {
		jsonMsg(c, "failed to read the exit list", err)
		return
	}
	slot := 0
	for _, exit := range exits {
		if exit.ID == exitID {
			slot = exit.Slot
		}
	}
	raw, err := vpngate.GetSupervisor().Client().DeleteExit(ctx, exitID)
	if err != nil {
		jsonMsg(c, "failed to delete the exit", err)
		return
	}
	if slot > 0 {
		if err := a.removeExitFromTemplate(slot); err != nil {
			jsonMsg(c, "exit deleted but Xray still references it", err)
			return
		}
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

func (a *VPNGateController) connectExit(c *gin.Context) {
	var form struct {
		ExitID string `json:"exitId"`
		NodeID string `json:"nodeId"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	exitID := strings.TrimSpace(form.ExitID)
	nodeID := strings.TrimSpace(form.NodeID)
	if nodeID == "" {
		jsonMsg(c, "a node id is required", errors.New("missing node id"))
		return
	}
	// A tunnel build plus its health probe takes tens of seconds.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	raw, err := vpngate.GetSupervisor().Client().ConnectExit(ctx, exitID, nodeID)
	if err != nil {
		jsonMsg(c, "failed to connect the exit", err)
		return
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

func (a *VPNGateController) autoConnectExit(c *gin.Context) {
	var form struct {
		ExitID string `json:"exitId"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	raw, err := vpngate.GetSupervisor().Client().AutoConnectExit(ctx, strings.TrimSpace(form.ExitID))
	if err != nil {
		jsonMsg(c, "failed to connect the exit", err)
		return
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

func (a *VPNGateController) disconnectExit(c *gin.Context) {
	var form struct {
		ExitID string `json:"exitId"`
	}
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	raw, err := vpngate.GetSupervisor().Client().DisconnectExit(ctx, strings.TrimSpace(form.ExitID))
	if err != nil {
		jsonMsg(c, "failed to disconnect the exit", err)
		return
	}
	decoded, _ := rawMessage(raw)
	jsonMsgObj(c, engineMessage(decoded), decoded, nil)
}

// bindExit sends one inbound through one exit (or back to direct).
func (a *VPNGateController) bindExit(c *gin.Context) {
	var form exitBindForm
	if err := c.ShouldBindJSON(&form); err != nil {
		jsonMsg(c, "invalid request body", err)
		return
	}
	tag := strings.TrimSpace(form.InboundTag)
	if tag == "" {
		jsonMsg(c, "an inbound tag is required", errors.New("missing inbound tag"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	exits, _, err := a.engineExits(ctx)
	if err != nil {
		jsonMsg(c, "failed to read the exit list", err)
		return
	}
	slot := 0
	exitID := strings.TrimSpace(form.ExitID)
	if exitID != "" && exitID != "direct" {
		found := false
		for _, exit := range exits {
			if exit.ID == exitID {
				slot = exit.Slot
				found = true
			}
		}
		if !found {
			jsonMsg(c, "unknown exit", fmt.Errorf("exit %q does not exist", exitID))
			return
		}
	}
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		jsonMsg(c, "failed to read the Xray template", err)
		return
	}
	if strings.TrimSpace(template) == "" {
		jsonMsg(c, "the Xray template is empty", errors.New("nothing to bind"))
		return
	}
	updated, changed, err := vpngate.BindExit(template, exits, tag, slot)
	if err != nil {
		jsonMsg(c, "failed to bind the inbound to the exit", err)
		return
	}
	if changed {
		if err := a.saveAndReloadXray(updated); err != nil {
			jsonMsg(c, "binding saved but Xray did not reload", err)
			return
		}
	}
	a.listExits(c)
}

// syncExits registers an outbound for every exit the engine reports, so the
// operator can point their own routing rules at vpngate-out-N too.
func (a *VPNGateController) syncExits(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), a.engineCallTimeout)
	defer cancel()
	if err := a.syncExitOutbounds(ctx); err != nil {
		jsonMsg(c, "failed to register the exit outbounds", err)
		return
	}
	a.listExits(c)
}

func (a *VPNGateController) syncExitOutbounds(ctx context.Context) error {
	exits, _, err := a.engineExits(ctx)
	if err != nil {
		return err
	}
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	if strings.TrimSpace(template) == "" {
		return nil
	}
	updated, changed, err := vpngate.EnsureExitOutbounds(template, exits)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return a.saveAndReloadXray(updated)
}

func (a *VPNGateController) removeExitFromTemplate(slot int) error {
	template, err := a.settingService.GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	if strings.TrimSpace(template) == "" {
		return nil
	}
	updated, changed, err := vpngate.RemoveExitOutbound(template, slot)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return a.saveAndReloadXray(updated)
}

func (a *VPNGateController) saveAndReloadXray(template string) error {
	if err := a.xraySetting.SaveXraySetting(template); err != nil {
		return err
	}
	if a.xrayService.IsXrayRunning() {
		return a.xrayService.RestartXray(false)
	}
	return nil
}
