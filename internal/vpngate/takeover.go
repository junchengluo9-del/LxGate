package vpngate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Takeover modes. They describe who decides which client traffic leaves through
// the VPNGate tunnel:
//
//	all      every inbound of this panel is sent to the VPNGate exit by one
//	         managed routing rule (no inboundTag: Xray matches all inbounds).
//	selected only the listed inbound tags are sent there.
//	manual   no managed rule at all; the operator writes their own routing.
const (
	TakeoverModeAll      = "all"
	TakeoverModeSelected = "selected"
	TakeoverModeManual   = "manual"
)

// TakeoverRuleTag marks the routing rule the panel owns. Xray keeps the field
// as rule metadata, so removing or rewriting "our" rule never touches a rule
// the operator added by hand, even when it also targets OutboundTag.
const TakeoverRuleTag = "vpngate-takeover"

// TakeoverSpec is the desired state accepted by POST /panel/api/vpngate/takeover.
type TakeoverSpec struct {
	Mode        string   `json:"mode"`
	InboundTags []string `json:"inboundTags"`
}

// TakeoverState is what the panel reads back: the mode that is actually in the
// Xray template, the inbound tags the managed rule covers, and whether the
// vpngate-out outbound exists.
type TakeoverState struct {
	Mode        string   `json:"mode"`
	InboundTags []string `json:"inboundTags"`
	Bound       bool     `json:"bound"`
	RuleTag     string   `json:"ruleTag"`
	// RuleIndex is the position of the managed rule in routing.rules, or -1.
	RuleIndex int `json:"ruleIndex"`
}

// NormalizeTakeoverMode lowercases and validates a mode, defaulting to manual
// for an empty value.
func NormalizeTakeoverMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return TakeoverModeManual, nil
	case TakeoverModeAll:
		return TakeoverModeAll, nil
	case TakeoverModeSelected:
		return TakeoverModeSelected, nil
	case TakeoverModeManual:
		return TakeoverModeManual, nil
	}
	return "", errors.New("mode must be one of all, selected, manual")
}

// CleanInboundTags trims, drops empty values and duplicates, and keeps the
// order stable so the stored rule does not churn between saves.
func CleanInboundTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// managedRule builds the routing rule for a mode. A selected rule with no tags
// is not representable (Xray reads an empty inboundTag as "match nothing"), so
// ApplyTakeover removes the rule instead.
func managedRule(spec TakeoverSpec) map[string]any {
	rule := map[string]any{
		"type":        "field",
		"network":     "tcp,udp",
		"outboundTag": OutboundTag,
		"ruleTag":     TakeoverRuleTag,
	}
	if spec.Mode == TakeoverModeSelected {
		tags := CleanInboundTags(spec.InboundTags)
		if len(tags) > 0 {
			rule["inboundTag"] = tags
		}
	}
	return rule
}

// ApplyTakeover rewrites the Xray config template so the requested takeover
// mode takes effect, and reports whether the document changed.
//
// Invariants this keeps:
//   - The vpngate-out socks outbound always exists when a mode needs it, so a
//     takeover rule can never point at a missing outbound (which Xray refuses
//     to load).
//   - The managed rule is placed *after* every rule the operator already had
//     that carries real criteria (API, DNS, private IPs, ad-blocking...) and
//     *before* a catch-all rule, so those keep winning while everything else
//     follows the takeover mode.
//   - Only the rule carrying TakeoverRuleTag is ever rewritten or removed.
//   - manual mode keeps the outbound in place: manual rules may still point at
//     vpngate-out.
func ApplyTakeover(templateJSON string, cfg Config, spec TakeoverSpec) (updated string, changed bool, err error) {
	mode, err := NormalizeTakeoverMode(spec.Mode)
	if err != nil {
		return templateJSON, false, err
	}
	spec.Mode = mode
	spec.InboundTags = CleanInboundTags(spec.InboundTags)

	// Register (or refresh) the outbound first: it is what every mode but a
	// bare manual mode relies on, and a manual mode may already use it.
	withOutbound, outboundChanged, err := EnsureOutbound(templateJSON, DefaultOutboundSpec(cfg))
	if err != nil {
		return templateJSON, false, err
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(withOutbound), &doc); err != nil {
		return templateJSON, false, fmt.Errorf("xray template is not valid JSON: %w", err)
	}
	routing, err := routingMap(doc)
	if err != nil {
		return templateJSON, false, err
	}
	rules, err := ruleSlice(routing)
	if err != nil {
		return templateJSON, false, err
	}

	kept := make([]any, 0, len(rules)+1)
	insertAt := -1
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if isManagedRule(rule) {
			if insertAt < 0 {
				insertAt = len(kept)
			}
			continue
		}
		kept = append(kept, raw)
	}

	if mode != TakeoverModeManual && (mode != TakeoverModeSelected || len(spec.InboundTags) > 0) {
		want := managedRule(spec)
		if insertAt < 0 {
			insertAt = takeoverInsertIndex(kept)
		}
		if insertAt > len(kept) {
			insertAt = len(kept)
		}
		kept = append(kept, nil)
		copy(kept[insertAt+1:], kept[insertAt:])
		kept[insertAt] = want
	}

	routing["rules"] = kept
	doc["routing"] = routing

	if !outboundChanged && equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return templateJSON, false, err
	}
	return string(encoded), true, nil
}

// ReadTakeover inspects a template and reports the takeover state it encodes.
// A template with no managed rule reads as manual even if it has rules that
// point at vpngate-out: those belong to the operator.
func ReadTakeover(templateJSON string) (TakeoverState, error) {
	state := TakeoverState{Mode: TakeoverModeManual, InboundTags: []string{}, RuleTag: TakeoverRuleTag, RuleIndex: -1}
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return state, fmt.Errorf("xray template is not valid JSON: %w", err)
	}
	state.Bound = HasOutbound(templateJSON, OutboundTag)
	routing, err := routingMap(doc)
	if err != nil {
		return state, err
	}
	rules, err := ruleSlice(routing)
	if err != nil {
		return state, err
	}
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok || !isManagedRule(rule) {
			continue
		}
		state.RuleIndex = i
		state.InboundTags = ruleInboundTags(rule)
		if len(state.InboundTags) == 0 {
			state.Mode = TakeoverModeAll
		} else {
			state.Mode = TakeoverModeSelected
		}
		break
	}
	return state, nil
}

// ManagedRuleIndex is the position of the managed rule in a template, or -1.
func ManagedRuleIndex(templateJSON string) int {
	state, err := ReadTakeover(templateJSON)
	if err != nil {
		return -1
	}
	return state.RuleIndex
}

func isManagedRule(rule map[string]any) bool {
	tag, _ := rule["ruleTag"].(string)
	return tag == TakeoverRuleTag
}

func ruleInboundTags(rule map[string]any) []string {
	raw, ok := rule["inboundTag"]
	if !ok {
		return []string{}
	}
	switch value := raw.(type) {
	case string:
		return CleanInboundTags([]string{value})
	case []any:
		tags := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return CleanInboundTags(tags)
	}
	return []string{}
}

// takeoverInsertIndex picks where the managed rule goes: just before the first
// catch-all rule (a rule with no match criteria at all), otherwise at the end.
func takeoverInsertIndex(rules []any) int {
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if isCatchAllRule(rule) {
			return i
		}
	}
	return len(rules)
}

// isCatchAllRule reports whether a rule matches everything, i.e. carries no
// criteria but an outbound/balancer target. "network": "tcp,udp" covers every
// protocol Xray proxies, so it counts as no criterion here.
func isCatchAllRule(rule map[string]any) bool {
	for key, value := range rule {
		switch key {
		case "type", "outboundTag", "balancerTag", "ruleTag":
			continue
		case "network":
			if coversAllNetworks(value) {
				continue
			}
			return false
		}
		if !isEmptyRuleValue(value) {
			return false
		}
	}
	_, hasOutbound := rule["outboundTag"]
	_, hasBalancer := rule["balancerTag"]
	return hasOutbound || hasBalancer
}

// coversAllNetworks reports whether a rule's network value names both tcp and udp.
func coversAllNetworks(value any) bool {
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		raw = strings.Join(parts, ",")
	default:
		return false
	}
	lowered := strings.ToLower(raw)
	return strings.Contains(lowered, "tcp") && strings.Contains(lowered, "udp")
}

func isEmptyRuleValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// SortedInboundTags is a helper for stable output in API responses and tests.
func SortedInboundTags(tags []string) []string {
	out := CleanInboundTags(tags)
	sort.Strings(out)
	return out
}

func routingMap(doc map[string]any) (map[string]any, error) {
	raw, ok := doc["routing"]
	if !ok || raw == nil {
		routing := map[string]any{"rules": []any{}}
		return routing, nil
	}
	routing, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("xray template routing is not an object")
	}
	return routing, nil
}

func ruleSlice(routing map[string]any) ([]any, error) {
	raw, ok := routing["rules"]
	if !ok || raw == nil {
		return []any{}, nil
	}
	rules, ok := raw.([]any)
	if !ok {
		return nil, errors.New("xray template routing.rules is not an array")
	}
	return rules, nil
}
