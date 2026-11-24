package vpngate

import (
	"encoding/json"
	"strings"
	"testing"
)

func exitTestSpecs() []ExitSpec {
	return []ExitSpec{
		{ID: "exit-1", Slot: 1, Name: "出口 1", ProxyPort: 7929, Dev: "lxgtun0", Table: 100},
		{ID: "exit-2", Slot: 2, Name: "出口 2", ProxyPort: 7930, Dev: "lxgtun1", Table: 101},
	}
}

const exitTestTemplate = `{
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block", "protocol": "blackhole"}
  ],
  "routing": {
    "rules": [
      {"type": "field", "domain": ["geosite:category-ads"], "outboundTag": "block"},
      {"type": "field", "network": "tcp,udp", "outboundTag": "direct"}
    ]
  }
}`

func rulesOf(t *testing.T, template string) []map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(template), &doc); err != nil {
		t.Fatalf("template is not JSON: %v", err)
	}
	routing, _ := doc["routing"].(map[string]any)
	raw, _ := routing["rules"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if rule, ok := entry.(map[string]any); ok {
			out = append(out, rule)
		}
	}
	return out
}

func outboundTags(t *testing.T, template string) []string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(template), &doc); err != nil {
		t.Fatalf("template is not JSON: %v", err)
	}
	raw, _ := doc["outbounds"].([]any)
	tags := make([]string, 0, len(raw))
	for _, entry := range raw {
		if m, ok := entry.(map[string]any); ok {
			tag, _ := m["tag"].(string)
			tags = append(tags, tag)
		}
	}
	return tags
}

func TestExitOutboundTagKeepsLegacyName(t *testing.T) {
	if got := ExitOutboundTag(1); got != "vpngate-out" {
		t.Fatalf("slot 1 tag = %q, want vpngate-out", got)
	}
	if got := ExitOutboundTag(2); got != "vpngate-out-2" {
		t.Fatalf("slot 2 tag = %q, want vpngate-out-2", got)
	}
	if got := ExitTagSlot("vpngate-out"); got != 1 {
		t.Fatalf("vpngate-out parsed as slot %d", got)
	}
	if got := ExitTagSlot("vpngate-out-7"); got != 7 {
		t.Fatalf("vpngate-out-7 parsed as slot %d", got)
	}
	// Operator tags that merely contain the prefix are not ours.
	for _, tag := range []string{"vpngate-outbound", "my-vpngate-out-2", "vpngate-out-x"} {
		if got := ExitTagSlot(tag); got != 0 {
			t.Fatalf("%q parsed as slot %d, want 0", tag, got)
		}
	}
	if ExitRuleTag(3) != "vpngate-exit-3" {
		t.Fatalf("rule tag = %q", ExitRuleTag(3))
	}
}

func TestEnsureExitOutboundsRegistersEveryExit(t *testing.T) {
	updated, changed, err := EnsureExitOutbounds(exitTestTemplate, exitTestSpecs())
	if err != nil {
		t.Fatalf("ensure failed: %v", err)
	}
	if !changed {
		t.Fatal("expected the template to change")
	}
	tags := outboundTags(t, updated)
	want := map[string]bool{"direct": true, "block": true, "vpngate-out": true, "vpngate-out-2": true}
	for _, tag := range tags {
		if !want[tag] {
			t.Fatalf("unexpected outbound %q in %v", tag, tags)
		}
		delete(want, tag)
	}
	if len(want) != 0 {
		t.Fatalf("missing outbounds: %v", want)
	}
	// The socks outbound points at that exit's own loopback proxy port.
	if !strings.Contains(updated, `"port": 7930`) {
		t.Fatalf("exit 2 outbound missing its proxy port: %s", updated)
	}

	if _, changedAgain, err := EnsureExitOutbounds(updated, exitTestSpecs()); err != nil || changedAgain {
		t.Fatalf("second ensure should be a no-op (changed=%v err=%v)", changedAgain, err)
	}
}

func TestEnsureExitOutboundsDropsStaleExitAndItsRules(t *testing.T) {
	withTwo, _, err := ApplyExitBindings(exitTestTemplate, exitTestSpecs(), map[string]int{"in-a": 2})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	updated, changed, err := EnsureExitOutbounds(withTwo, exitTestSpecs()[:1])
	if err != nil || !changed {
		t.Fatalf("ensure failed (changed=%v): %v", changed, err)
	}
	for _, tag := range outboundTags(t, updated) {
		if tag == "vpngate-out-2" {
			t.Fatal("stale outbound vpngate-out-2 survived")
		}
	}
	for _, rule := range rulesOf(t, updated) {
		if tag, _ := rule["outboundTag"].(string); tag == "vpngate-out-2" {
			t.Fatal("a rule still points at the removed outbound")
		}
	}
}

func TestApplyExitBindingsGroupsByExitInOrder(t *testing.T) {
	bindings := map[string]int{"in-b": 2, "in-a": 2, "in-c": 1}
	updated, changed, err := ApplyExitBindings(exitTestTemplate, exitTestSpecs(), bindings)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	rules := rulesOf(t, updated)
	// Operator rules keep their place; managed rules sit before the catch-all.
	if ruleTag, _ := rules[0]["ruleTag"].(string); ruleTag != "" {
		t.Fatalf("operator rule was displaced: %+v", rules[0])
	}
	var exitRules []map[string]any
	for _, rule := range rules {
		if tag, _ := rule["ruleTag"].(string); strings.HasPrefix(tag, ExitRulePrefix) {
			exitRules = append(exitRules, rule)
		}
	}
	if len(exitRules) != 2 {
		t.Fatalf("want 2 managed rules, got %d (%+v)", len(exitRules), rules)
	}
	if exitRules[0]["ruleTag"] != "vpngate-exit-1" || exitRules[0]["outboundTag"] != "vpngate-out" {
		t.Fatalf("exit 1 rule = %+v", exitRules[0])
	}
	if exitRules[1]["ruleTag"] != "vpngate-exit-2" || exitRules[1]["outboundTag"] != "vpngate-out-2" {
		t.Fatalf("exit 2 rule = %+v", exitRules[1])
	}
	tags, _ := exitRules[1]["inboundTag"].([]any)
	if len(tags) != 2 || tags[0] != "in-a" || tags[1] != "in-b" {
		t.Fatalf("exit 2 inbound tags = %v, want sorted [in-a in-b]", tags)
	}

	read := ReadExitBindings(updated)
	if read["in-a"] != 2 || read["in-b"] != 2 || read["in-c"] != 1 {
		t.Fatalf("read back %v", read)
	}

	if _, changedAgain, err := ApplyExitBindings(updated, exitTestSpecs(), bindings); err != nil || changedAgain {
		t.Fatalf("re-apply should be a no-op (changed=%v err=%v)", changedAgain, err)
	}
}

func TestApplyExitBindingsWinsOverTakeoverRule(t *testing.T) {
	// An operator turned on "all inbounds" earlier; a per-inbound choice must
	// still take effect, which means the managed exit rules come first.
	withTakeover, _, err := ApplyTakeover(exitTestTemplate, Config{ProxyPort: 7929}, TakeoverSpec{Mode: TakeoverModeAll})
	if err != nil {
		t.Fatalf("takeover failed: %v", err)
	}
	updated, _, err := ApplyExitBindings(withTakeover, exitTestSpecs(), map[string]int{"in-a": 2})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	rules := rulesOf(t, updated)
	exitIdx, takeoverIdx := -1, -1
	for i, rule := range rules {
		tag, _ := rule["ruleTag"].(string)
		if strings.HasPrefix(tag, ExitRulePrefix) {
			exitIdx = i
		}
		if tag == TakeoverRuleTag {
			takeoverIdx = i
		}
	}
	if exitIdx < 0 || takeoverIdx < 0 {
		t.Fatalf("missing rules: exit=%d takeover=%d (%+v)", exitIdx, takeoverIdx, rules)
	}
	if exitIdx > takeoverIdx {
		t.Fatalf("per-inbound rule at %d must come before the all-inbounds rule at %d", exitIdx, takeoverIdx)
	}
}

func TestBindExitAndDirectAgain(t *testing.T) {
	bound, changed, err := BindExit(exitTestTemplate, exitTestSpecs(), "in-a", 2)
	if err != nil || !changed {
		t.Fatalf("bind failed (changed=%v): %v", changed, err)
	}
	if ReadExitBindings(bound)["in-a"] != 2 {
		t.Fatal("binding not recorded")
	}
	// "直接" (direct) is the absence of a binding, not a special outbound.
	direct, changed, err := BindExit(bound, exitTestSpecs(), "in-a", 0)
	if err != nil || !changed {
		t.Fatalf("unbind failed (changed=%v): %v", changed, err)
	}
	if slot, ok := ReadExitBindings(direct)["in-a"]; ok {
		t.Fatalf("inbound still bound to slot %d", slot)
	}
	for _, rule := range rulesOf(t, direct) {
		if tag, _ := rule["ruleTag"].(string); strings.HasPrefix(tag, ExitRulePrefix) {
			t.Fatalf("empty managed rule left behind: %+v", rule)
		}
	}
	// The outbounds stay registered: Xray outbounds are not per-inbound state.
	if !HasOutbound(direct, ExitOutboundTag(2)) {
		t.Fatal("unbinding an inbound removed the exit outbound")
	}
}

func TestBindExitRejectsUnknownSlot(t *testing.T) {
	// A slot the engine no longer reports must not produce a rule pointing at a
	// tag nobody registers.
	base, _, err := EnsureExitOutbounds(exitTestTemplate, exitTestSpecs())
	if err != nil {
		t.Fatalf("ensure failed: %v", err)
	}
	updated, changed, err := BindExit(base, exitTestSpecs(), "in-a", 9)
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if changed {
		t.Fatalf("unknown slot should be dropped, got change: %s", updated)
	}
	if _, ok := ReadExitBindings(updated)["in-a"]; ok {
		t.Fatal("unknown slot was bound")
	}
}

func TestRemoveExitOutboundLeavesOtherExitsAlone(t *testing.T) {
	withBoth, _, err := ApplyExitBindings(exitTestTemplate, exitTestSpecs(), map[string]int{"in-a": 1, "in-b": 2})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	updated, changed, err := RemoveExitOutbound(withBoth, 2)
	if err != nil || !changed {
		t.Fatalf("remove failed (changed=%v): %v", changed, err)
	}
	if HasOutbound(updated, ExitOutboundTag(2)) {
		t.Fatal("exit 2 outbound survived")
	}
	if !HasOutbound(updated, ExitOutboundTag(1)) {
		t.Fatal("exit 1 outbound was removed")
	}
	read := ReadExitBindings(updated)
	if read["in-a"] != 1 {
		t.Fatalf("exit 1 binding lost: %v", read)
	}
	if _, ok := read["in-b"]; ok {
		t.Fatalf("exit 2 binding survived: %v", read)
	}
	for _, rule := range rulesOf(t, updated) {
		if tag, _ := rule["ruleTag"].(string); tag == "vpngate-exit-2" {
			t.Fatal("exit 2 rule survived")
		}
	}
}

func TestParseExits(t *testing.T) {
	raw := []byte(`{"ok": true, "exits": [{"id":"exit-1","slot":1,"proxyPort":7929},{"id":"exit-2","slot":2,"proxyPort":7930}], "memory": {}}`)
	exits, err := ParseExits(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(exits) != 2 || exits[1].ID != "exit-2" || exits[1].ProxyPort != 7930 {
		t.Fatalf("parsed %+v", exits)
	}
	if _, err := ParseExits([]byte(`{"ok": true}`)); err == nil {
		t.Fatal("a payload without exits should be an error, not an empty list")
	}
}
