package vpngate

import (
	"encoding/json"
	"strings"
	"testing"
)

const baseTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [],
  "outbounds": [
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "blocked", "protocol": "blackhole"}
  ],
  "routing": {"rules": []}
}`

func decodeTemplate(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("updated template is not valid JSON: %v", err)
	}
	return doc
}

func findOutbound(doc map[string]any, tag string) map[string]any {
	outbounds, _ := doc["outbounds"].([]any)
	for _, entry := range outbounds {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := m["tag"].(string); got == tag {
			return m
		}
	}
	return nil
}

func TestEnsureOutboundAppends(t *testing.T) {
	spec := OutboundSpec{Address: "127.0.0.1", Port: 7928, Tag: OutboundTag}
	updated, changed, err := EnsureOutbound(baseTemplate, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !changed {
		t.Fatal("first bind must report a change")
	}
	doc := decodeTemplate(t, updated)
	bound := findOutbound(doc, OutboundTag)
	if bound == nil {
		t.Fatalf("outbound %q missing from %s", OutboundTag, updated)
	}
	if bound["protocol"] != "socks" {
		t.Fatalf("protocol = %v", bound["protocol"])
	}
	settings, _ := bound["settings"].(map[string]any)
	servers, _ := settings["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("servers = %#v", servers)
	}
	server, _ := servers[0].(map[string]any)
	if server["address"] != "127.0.0.1" || server["port"].(float64) != 7928 {
		t.Fatalf("server = %#v", server)
	}
	// Existing outbounds survive untouched.
	if findOutbound(doc, "direct") == nil || findOutbound(doc, "blocked") == nil {
		t.Fatal("existing outbounds were dropped")
	}
}

func TestEnsureOutboundIsIdempotent(t *testing.T) {
	spec := OutboundSpec{Address: "127.0.0.1", Port: 7928}
	first, _, err := EnsureOutbound(baseTemplate, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	second, changed, err := EnsureOutbound(first, spec)
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if changed {
		t.Fatal("re-binding an identical outbound must not report a change")
	}
	if second != first {
		t.Fatal("re-binding must return the document unchanged")
	}
}

func TestEnsureOutboundRefreshesPort(t *testing.T) {
	spec := OutboundSpec{Address: "127.0.0.1", Port: 7928}
	first, _, err := EnsureOutbound(baseTemplate, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	updated, changed, err := EnsureOutbound(first, OutboundSpec{Address: "127.0.0.1", Port: 7999})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !changed {
		t.Fatal("a new proxy port must update the outbound")
	}
	doc := decodeTemplate(t, updated)
	settings, _ := findOutbound(doc, OutboundTag)["settings"].(map[string]any)
	servers, _ := settings["servers"].([]any)
	server, _ := servers[0].(map[string]any)
	if server["port"].(float64) != 7999 {
		t.Fatalf("port = %v", server["port"])
	}
	outbounds, _ := doc["outbounds"].([]any)
	if len(outbounds) != 3 {
		t.Fatalf("outbound count = %d, want 3 (no duplicate tag)", len(outbounds))
	}
}

func TestEnsureOutboundRejectsInvalidJSON(t *testing.T) {
	if _, _, err := EnsureOutbound("{not json", OutboundSpec{}); err == nil {
		t.Fatal("invalid template must be rejected")
	}
}

func TestEnsureOutboundRejectsNonArrayOutbounds(t *testing.T) {
	if _, _, err := EnsureOutbound(`{"outbounds": "nope"}`, OutboundSpec{}); err == nil {
		t.Fatal("non-array outbounds must be rejected")
	}
}

func TestRemoveOutboundDropsRules(t *testing.T) {
	template := `{
      "outbounds": [
        {"tag": "direct", "protocol": "freedom"},
        {"tag": "vpngate-out", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 7928}]}}
      ],
      "routing": {"rules": [
        {"type": "field", "domain": ["geosite:google"], "outboundTag": "vpngate-out"},
        {"type": "field", "domain": ["geosite:cn"], "outboundTag": "direct"}
      ]}
    }`
	updated, changed, err := RemoveOutbound(template, OutboundTag)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !changed {
		t.Fatal("removal must report a change")
	}
	doc := decodeTemplate(t, updated)
	if findOutbound(doc, OutboundTag) != nil {
		t.Fatal("outbound still present")
	}
	if findOutbound(doc, "direct") == nil {
		t.Fatal("unrelated outbound removed")
	}
	routing, _ := doc["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %#v, want the vpngate rule dropped", rules)
	}
	rule, _ := rules[0].(map[string]any)
	if rule["outboundTag"] != "direct" {
		t.Fatalf("wrong rule kept: %#v", rule)
	}
}

func TestRemoveOutboundNoop(t *testing.T) {
	updated, changed, err := RemoveOutbound(baseTemplate, OutboundTag)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if changed {
		t.Fatal("removing a missing outbound must not report a change")
	}
	if updated != baseTemplate {
		t.Fatal("template must come back untouched")
	}
}

func TestHasOutbound(t *testing.T) {
	if HasOutbound(baseTemplate, OutboundTag) {
		t.Fatal("template has no vpngate outbound yet")
	}
	updated, _, err := EnsureOutbound(baseTemplate, OutboundSpec{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !HasOutbound(updated, OutboundTag) {
		t.Fatal("bound outbound not detected")
	}
	if HasOutbound("{broken", OutboundTag) {
		t.Fatal("invalid JSON must not report a binding")
	}
}

func TestRoutingRuleSnippet(t *testing.T) {
	snippet := RoutingRuleSnippet(OutboundTag)
	if !strings.Contains(snippet, `"outboundTag": "vpngate-out"`) {
		t.Fatalf("snippet = %s", snippet)
	}
	if _, err := json.Marshal([]byte(snippet)); err != nil {
		t.Fatalf("snippet must be JSON: %v", err)
	}
}

func TestDefaultOutboundSpec(t *testing.T) {
	spec := DefaultOutboundSpec(Config{APIPort: 18787, ProxyPort: 7928})
	if spec.Address != "127.0.0.1" || spec.Port != 7928 || spec.Tag != OutboundTag {
		t.Fatalf("spec = %#v", spec)
	}
}
