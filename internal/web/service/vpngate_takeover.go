package service

import (
	"encoding/json"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/vpngate"
)

// vpngateTakeoverKey stores the operator's desired takeover state as JSON.
//
// The Xray template already encodes the *effective* state (the managed routing
// rule), but it cannot encode two things: "selected with nothing chosen yet"
// (an empty inboundTag list is not a legal Xray rule) and "manual, but remember
// the last selection" (the rule is gone in manual mode). The key closes that gap
// so GET /panel/api/vpngate/takeover can always answer with the mode the
// operator picked.
const vpngateTakeoverKey = "vpngateTakeover"

// GetVPngateTakeover reads the stored takeover state. A fresh install has none,
// which reads as manual.
func (s *SettingService) GetVPngateTakeover() (vpngate.TakeoverSpec, error) {
	spec := vpngate.TakeoverSpec{Mode: vpngate.TakeoverModeManual, InboundTags: []string{}}
	setting, err := s.getSetting(vpngateTakeoverKey)
	if database.IsNotFound(err) {
		return spec, nil
	}
	if err != nil {
		return spec, err
	}
	if strings.TrimSpace(setting.Value) == "" {
		return spec, nil
	}
	var stored vpngate.TakeoverSpec
	if err := json.Unmarshal([]byte(setting.Value), &stored); err != nil {
		return spec, nil
	}
	mode, err := vpngate.NormalizeTakeoverMode(stored.Mode)
	if err != nil {
		return spec, nil
	}
	spec.Mode = mode
	spec.InboundTags = vpngate.CleanInboundTags(stored.InboundTags)
	return spec, nil
}

// SetVPngateTakeover persists the desired takeover state.
func (s *SettingService) SetVPngateTakeover(spec vpngate.TakeoverSpec) error {
	mode, err := vpngate.NormalizeTakeoverMode(spec.Mode)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(vpngate.TakeoverSpec{
		Mode:        mode,
		InboundTags: vpngate.CleanInboundTags(spec.InboundTags),
	})
	if err != nil {
		return err
	}
	return s.saveSetting(vpngateTakeoverKey, string(encoded))
}

// InboundExitOption is the lightweight projection the VPNGate page needs to
// list an inbound's exit choice: identity plus the bits the operator recognises
// it by. Client settings and traffic counters are deliberately absent.
type InboundExitOption struct {
	Id       int    `json:"id"`
	Tag      string `json:"tag"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Enable   bool   `json:"enable"`
	NodeID   *int   `json:"nodeId,omitempty"`
}

// GetInboundExitOptions lists every inbound of the user in the order the
// inbounds page shows them.
func (s *InboundService) GetInboundExitOptions(userId int) ([]InboundExitOption, error) {
	db := database.GetDB()
	var rows []InboundExitOption
	err := db.Table("inbounds").
		Select("id, tag, remark, protocol, port, enable, node_id").
		Where("user_id = ?", userId).
		Order("id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []InboundExitOption{}
	}
	return rows, nil
}
