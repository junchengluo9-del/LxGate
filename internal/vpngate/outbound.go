package vpngate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// OutboundTag is the fixed tag the panel registers for the VPNGate exit. The
// engine rewrites its own SOCKS listener in place, so a Xray config that points
// at this tag never needs a restart when the engine switches exit nodes.
const OutboundTag = "vpngate-out"

// OutboundSpec describes the socks outbound that targets the embedded proxy.
type OutboundSpec struct {
	Address string
	Port    int
	Tag     string
	// Users, when set, asks the local proxy for username/password auth. The
	// embedded engine's proxy is loopback-only and unauthenticated by default.
	Username string
	Password string
}

// DefaultOutboundSpec is the canonical binding for cfg.
func DefaultOutboundSpec(cfg Config) OutboundSpec {
	return OutboundSpec{Address: "127.0.0.1", Port: cfg.ProxyPort, Tag: OutboundTag}
}

func (s OutboundSpec) normalized() OutboundSpec {
	if s.Tag == "" {
		s.Tag = OutboundTag
	}
	if strings.TrimSpace(s.Address) == "" {
		s.Address = "127.0.0.1"
	}
	if s.Port <= 0 {
		s.Port = DefaultProxyPort
	}
	return s
}

func (s OutboundSpec) toMap() map[string]any {
	s = s.normalized()
	server := map[string]any{"address": s.Address, "port": s.Port}
	if s.Username != "" {
		server["users"] = []any{map[string]any{"user": s.Username, "pass": s.Password}}
	}
	return map[string]any{
		"tag":      s.Tag,
		"protocol": "socks",
		"settings": map[string]any{
			"servers": []any{server},
		},
	}
}

// EnsureOutbound inserts or refreshes the VPNGate socks outbound in a Xray
// config JSON document. It reports whether the document changed, and leaves
// every other key (including comments-free formatting) semantically intact.
func EnsureOutbound(templateJSON string, spec OutboundSpec) (updated string, changed bool, err error) {
	spec = spec.normalized()
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return templateJSON, false, fmt.Errorf("xray template is not valid JSON: %w", err)
	}
	outbounds, err := outboundSlice(doc)
	if err != nil {
		return templateJSON, false, err
	}

	want := spec.toMap()
	replaced := false
	for i, entry := range outbounds {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); tag == spec.Tag {
			outbounds[i] = want
			replaced = true
			break
		}
	}
	if !replaced {
		outbounds = append(outbounds, want)
	}
	doc["outbounds"] = outbounds

	if equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return templateJSON, false, err
	}
	return string(encoded), true, nil
}

// RemoveOutbound drops the VPNGate outbound and any routing rule that targets
// it, so unbinding cannot leave Xray with a dangling outboundTag.
func RemoveOutbound(templateJSON string, tag string) (updated string, changed bool, err error) {
	if tag == "" {
		tag = OutboundTag
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return templateJSON, false, fmt.Errorf("xray template is not valid JSON: %w", err)
	}
	outbounds, err := outboundSlice(doc)
	if err != nil {
		return templateJSON, false, err
	}
	kept := make([]any, 0, len(outbounds))
	for _, entry := range outbounds {
		m, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		if tagOf, _ := m["tag"].(string); tagOf == tag {
			continue
		}
		kept = append(kept, entry)
	}
	doc["outbounds"] = kept

	if routing, ok := doc["routing"].(map[string]any); ok {
		if rules, ok := routing["rules"].([]any); ok {
			keptRules := make([]any, 0, len(rules))
			for _, rule := range rules {
				if m, ok := rule.(map[string]any); ok {
					if outboundTag, _ := m["outboundTag"].(string); outboundTag == tag {
						continue
					}
				}
				keptRules = append(keptRules, rule)
			}
			routing["rules"] = keptRules
			doc["routing"] = routing
		}
	}

	if equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return templateJSON, false, err
	}
	return string(encoded), true, nil
}

// HasOutbound reports whether a Xray config already binds tag.
func HasOutbound(templateJSON string, tag string) bool {
	if tag == "" {
		tag = OutboundTag
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return false
	}
	outbounds, err := outboundSlice(doc)
	if err != nil {
		return false
	}
	for _, entry := range outbounds {
		if m, ok := entry.(map[string]any); ok {
			if got, _ := m["tag"].(string); got == tag {
				return true
			}
		}
	}
	return false
}

// RoutingRuleSnippet is the rule a user can paste into routing to send chosen
// traffic through the VPNGate exit without touching the outbound again.
func RoutingRuleSnippet(tag string) string {
	if tag == "" {
		tag = OutboundTag
	}
	return fmt.Sprintf(`{"type": "field", "network": "tcp,udp", "domain": ["geosite:google"], "outboundTag": "%s"}`, tag)
}

func outboundSlice(doc map[string]any) ([]any, error) {
	raw, ok := doc["outbounds"]
	if !ok || raw == nil {
		return []any{}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("xray template outbounds is not an array")
	}
	return list, nil
}

func equalJSON(original string, doc map[string]any) bool {
	encoded, err := json.Marshal(doc)
	if err != nil {
		return false
	}
	var a, b any
	if err := json.Unmarshal([]byte(original), &a); err != nil {
		return false
	}
	if err := json.Unmarshal(encoded, &b); err != nil {
		return false
	}
	left, err1 := json.Marshal(a)
	right, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(left) == string(right)
}
