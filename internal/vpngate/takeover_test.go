package vpngate

import (
	"encoding/json"
	"testing"
)

const routingTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {"tag": "in-vless", "port": 443, "protocol": "vless"},
    {"tag": "in-ss", "port": 8388, "protocol": "shadowsocks"}
  ],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "blocked", "protocol": "blackhole"}
  ],
  "routing": {"domainStrategy": "AsIs", "rules": [
    {"type": "field", "inboundTag": ["api"], "outboundTag": "api"},
    {"type": "field", "ip": ["geoip:private"], "outboundTag": "blocked"},
    {"type": "field", "network": "tcp,udp", "outboundTag": "direct"}
  ]}
}`

func managedRuleOf(t *testing.T, raw string) (map[string]any, int) {
	t.Helper()
	doc := decodeTemplate(t, raw)
	routing, ok := doc["routing"].(map[string]any)
	if !ok {
		t.Fatalf("routing missing from %s", raw)
	}
	rules, _ := routing["rules"].([]any)
	for i, entry := range rules {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := rule["ruleTag"].(string); tag == TakeoverRuleTag {
			return rule, i
		}
	}
	return nil, -1
}

func TestApplyTakeoverAllPlacesRuleBeforeCatchAll(t *testing.T) {
	cfg := Config{ProxyPort: 7929}
	updated, changed, err := ApplyTakeover(routingTemplate, cfg, TakeoverSpec{Mode: TakeoverModeAll})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("first takeover must change the template")
	}
	doc := decodeTemplate(t, updated)
	if findOutbound(doc, OutboundTag) == nil {
		t.Fatal("takeover must register the socks outbound")
	}
	rule, index := managedRuleOf(t, updated)
	if rule == nil {
		t.Fatalf("managed rule missing from %s", updated)
	}
	if rule["outboundTag"] != OutboundTag {
		t.Fatalf("outboundTag = %v", rule["outboundTag"])
	}
	if _, hasInbound := rule["inboundTag"]; hasInbound {
		t.Fatalf("all mode must match every inbound, got %v", rule["inboundTag"])
	}
	// api rule, private-ip rule, then the takeover rule, then the catch-all.
	if index != 2 {
		t.Fatalf("managed rule index = %d, want 2 (before the catch-all)", index)
	}
	catchAll := doc["routing"].(map[string]any)["rules"].([]any)[3].(map[string]any)
	if catchAll["outboundTag"] != "direct" {
		t.Fatalf("catch-all moved: %v", catchAll)
	}

	state, err := ReadTakeover(updated)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if state.Mode != TakeoverModeAll || !state.Bound {
		t.Fatalf("state = %+v", state)
	}

	// Applying again is a no-op, so the UI can poll without churning Xray.
	if _, again, err := ApplyTakeover(updated, cfg, TakeoverSpec{Mode: TakeoverModeAll}); err != nil {
		t.Fatalf("reapply: %v", err)
	} else if again {
		t.Fatal("reapplying the same mode must not report a change")
	}
}

func TestApplyTakeoverSelectedKeepsPrivateRules(t *testing.T) {
	cfg := Config{ProxyPort: 7929}
	updated, changed, err := ApplyTakeover(routingTemplate, cfg, TakeoverSpec{
		Mode:        TakeoverModeSelected,
		InboundTags: []string{" in-vless ", "in-vless", "in-ss"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("selected takeover must change the template")
	}
	rule, index := managedRuleOf(t, updated)
	tags, _ := rule["inboundTag"].([]any)
	if len(tags) != 2 || tags[0] != "in-vless" || tags[1] != "in-ss" {
		t.Fatalf("inboundTag = %v, want the two cleaned tags", rule["inboundTag"])
	}
	if index != 2 {
		t.Fatalf("managed rule index = %d", index)
	}
	state, _ := ReadTakeover(updated)
	if state.Mode != TakeoverModeSelected {
		t.Fatalf("mode = %s", state.Mode)
	}
	if len(state.InboundTags) != 2 {
		t.Fatalf("tags = %v", state.InboundTags)
	}
}

func TestApplyTakeoverSelectedUpdatesTagsInPlace(t *testing.T) {
	cfg := Config{ProxyPort: 7929}
	first, _, err := ApplyTakeover(routingTemplate, cfg, TakeoverSpec{Mode: TakeoverModeSelected, InboundTags: []string{"in-vless"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	second, changed, err := ApplyTakeover(first, cfg, TakeoverSpec{Mode: TakeoverModeSelected, InboundTags: []string{"in-ss"}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("changing the selection must change the template")
	}
	rule, index := managedRuleOf(t, second)
	tags, _ := rule["inboundTag"].([]any)
	if len(tags) != 1 || tags[0] != "in-ss" {
		t.Fatalf("inboundTag = %v", rule["inboundTag"])
	}
	if index != 2 {
		t.Fatalf("managed rule moved to %d", index)
	}
}

func TestApplyTakeoverManualRemovesOnlyManagedRule(t *testing.T) {
	cfg := Config{ProxyPort: 7929}
	withRule, _, err := ApplyTakeover(routingTemplate, cfg, TakeoverSpec{Mode: TakeoverModeAll})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// An operator's own rule pointing at the same outbound must survive.
	doc := decodeTemplate(t, withRule)
	routing := doc["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	rules = append(rules, map[string]any{
		"type": "field", "domain": []any{"geosite:netflix"}, "outboundTag": OutboundTag,
	})
	routing["rules"] = rules
	doc["routing"] = routing
	custom, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	manual, changed, err := ApplyTakeover(string(custom), cfg, TakeoverSpec{Mode: TakeoverModeManual})
	if err != nil {
		t.Fatalf("apply manual: %v", err)
	}
	if !changed {
		t.Fatal("switching to manual must remove the managed rule")
	}
	if rule, _ := managedRuleOf(t, manual); rule != nil {
		t.Fatal("managed rule still present after switching to manual")
	}
	kept := decodeTemplate(t, manual)
	keptRules := kept["routing"].(map[string]any)["rules"].([]any)
	found := false
	for _, entry := range keptRules {
		rule, _ := entry.(map[string]any)
		if rule == nil {
			continue
		}
		if domains, ok := rule["domain"].([]any); ok && len(domains) == 1 && domains[0] == "geosite:netflix" {
			found = true
		}
	}
	if !found {
		t.Fatalf("operator rule was removed: %s", manual)
	}
	if findOutbound(kept, OutboundTag) == nil {
		t.Fatal("manual mode must keep the outbound manual rules may point at")
	}
	state, _ := ReadTakeover(manual)
	if state.Mode != TakeoverModeManual {
		t.Fatalf("mode = %s", state.Mode)
	}
}

func TestApplyTakeoverSelectedWithoutTagsClearsRule(t *testing.T) {
	cfg := Config{ProxyPort: 7929}
	withRule, _, err := ApplyTakeover(routingTemplate, cfg, TakeoverSpec{Mode: TakeoverModeAll})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	cleared, changed, err := ApplyTakeover(withRule, cfg, TakeoverSpec{Mode: TakeoverModeSelected})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !changed {
		t.Fatal("emptying the selection must remove the managed rule")
	}
	if rule, _ := managedRuleOf(t, cleared); rule != nil {
		t.Fatal("empty selection must not leave a rule behind")
	}
}

func TestApplyTakeoverSkipsMalformedManagedEntry(t *testing.T) {
	// A managed rule without a catch-all after it is appended at the end.
	template := `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[
		{"type":"field","inboundTag":["api"],"outboundTag":"api"}
	]}}`
	updated, _, err := ApplyTakeover(template, Config{ProxyPort: 7929}, TakeoverSpec{Mode: TakeoverModeAll})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, index := managedRuleOf(t, updated); index != 1 {
		t.Fatalf("managed rule index = %d, want appended at 1", index)
	}
}

func TestApplyTakeoverRejectsUnknownMode(t *testing.T) {
	if _, _, err := ApplyTakeover(routingTemplate, Config{ProxyPort: 7929}, TakeoverSpec{Mode: "everything"}); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}

func TestReadTakeoverWithoutRouting(t *testing.T) {
	state, err := ReadTakeover(`{"outbounds":[]}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if state.Mode != TakeoverModeManual || state.RuleIndex != -1 {
		t.Fatalf("state = %+v", state)
	}
}
