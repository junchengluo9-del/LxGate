package vpngate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Multi-exit wiring.
//
// The engine owns N independent tunnels (exit-1 … exit-N). Each one needs its
// own socks outbound in the Xray template, and each inbound picks exactly one
// of them (or none, i.e. direct). Both facts live in the template:
//
//	outbounds:  vpngate-out (exit 1, kept for backward compatibility)
//	            vpngate-out-2, vpngate-out-3, … (exit 2, 3, …)
//	rules:      one panel-owned rule per exit, ruleTag "vpngate-exit-<slot>",
//	            inboundTag: [<the inbounds bound to that exit>]
//
// Deriving the binding from the rules means the page, the inbound form and Xray
// itself can never disagree, and deleting an exit is a pure template rewrite.
const (
	// ExitOutboundPrefix is shared by every managed outbound tag.
	ExitOutboundPrefix = "vpngate-out"
	// ExitRulePrefix marks the panel-owned per-exit routing rules.
	ExitRulePrefix = "vpngate-exit-"
)

// vpngateOutboundTag matches exactly the tags this package manages:
// "vpngate-out" and "vpngate-out-<slot>".
var vpngateOutboundTag = regexp.MustCompile(`^vpngate-out(-\d+)?$`)

// ExitSpec is one exit as the engine describes it, reduced to what the Xray
// template needs.
type ExitSpec struct {
	ID        string `json:"id"`
	Slot      int    `json:"slot"`
	Name      string `json:"name"`
	ProxyPort int    `json:"proxyPort"`
	Dev       string `json:"dev"`
	Table     int    `json:"table"`
	Running   bool   `json:"running"`
	NodeID    string `json:"nodeId"`
	Stage     string `json:"stage"`
}

// ExitOutboundTag returns the socks outbound tag for a slot. Slot 1 keeps the
// historical tag so an existing template keeps working after the upgrade.
func ExitOutboundTag(slot int) string {
	if slot <= 1 {
		return OutboundTag
	}
	return fmt.Sprintf("%s-%d", ExitOutboundPrefix, slot)
}

// ExitRuleTag returns the ruleTag of the managed rule that binds inbounds to a
// slot's outbound.
func ExitRuleTag(slot int) string {
	return fmt.Sprintf("%s%d", ExitRulePrefix, slot)
}

// ExitTagSlot parses a managed outbound tag back into a slot (1 for the legacy
// tag, 0 when the tag is not ours).
func ExitTagSlot(tag string) int {
	if !vpngateOutboundTag.MatchString(tag) {
		return 0
	}
	rest := strings.TrimPrefix(tag, ExitOutboundPrefix)
	if rest == "" {
		return 1
	}
	slot, err := strconv.Atoi(strings.TrimPrefix(rest, "-"))
	if err != nil || slot <= 0 {
		return 0
	}
	return slot
}

// ExitSpecsFrom builds the template-side view of a slot list, skipping entries
// without a usable port (the engine always sends one; this only guards a
// malformed answer).
func ExitSpecsFrom(exits []ExitSpec) []ExitSpec {
	out := make([]ExitSpec, 0, len(exits))
	for _, exit := range exits {
		if exit.Slot <= 0 || exit.ProxyPort <= 0 || exit.ProxyPort > 65535 {
			continue
		}
		out = append(out, exit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// EnsureExitOutbounds registers (or refreshes) one socks outbound per exit and
// removes the ones whose exit is gone, together with every rule that pointed at
// a removed tag. Xray refuses to load a config with a dangling outboundTag, so
// the two halves always move together.
func EnsureExitOutbounds(templateJSON string, exits []ExitSpec) (updated string, changed bool, err error) {
	exits = ExitSpecsFrom(exits)
	wanted := make(map[string]ExitSpec, len(exits))
	for _, exit := range exits {
		wanted[ExitOutboundTag(exit.Slot)] = exit
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return templateJSON, false, fmt.Errorf("xray template is not valid JSON: %w", err)
	}
	outbounds, err := outboundSlice(doc)
	if err != nil {
		return templateJSON, false, err
	}

	removed := make(map[string]struct{})
	kept := make([]any, 0, len(outbounds)+len(exits))
	seen := make(map[string]bool, len(exits))
	for _, entry := range outbounds {
		m, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		tag, _ := m["tag"].(string)
		if ExitTagSlot(tag) > 0 {
			spec, keep := wanted[tag]
			if !keep {
				removed[tag] = struct{}{}
				continue
			}
			kept = append(kept, OutboundSpec{
				Address: "127.0.0.1",
				Port:    spec.ProxyPort,
				Tag:     tag,
			}.toMap())
			seen[tag] = true
			continue
		}
		kept = append(kept, entry)
	}
	for _, exit := range exits {
		tag := ExitOutboundTag(exit.Slot)
		if seen[tag] {
			continue
		}
		kept = append(kept, OutboundSpec{Address: "127.0.0.1", Port: exit.ProxyPort, Tag: tag}.toMap())
	}
	doc["outbounds"] = kept

	if len(removed) > 0 {
		routing, routingErr := routingMap(doc)
		if routingErr != nil {
			return templateJSON, false, routingErr
		}
		rules, ruleErr := ruleSlice(routing)
		if ruleErr != nil {
			return templateJSON, false, ruleErr
		}
		keptRules := make([]any, 0, len(rules))
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				keptRules = append(keptRules, raw)
				continue
			}
			if tag, _ := rule["outboundTag"].(string); isRemovedTag(tag, removed) {
				continue
			}
			if slot := exitRuleSlot(rule); slot > 0 && !hasSlot(exits, slot) {
				continue
			}
			keptRules = append(keptRules, raw)
		}
		routing["rules"] = keptRules
		doc["routing"] = routing
	}

	if equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	return encodeDoc(doc)
}

// ApplyExitBindings rebuilds every panel-owned per-inbound exit rule from
// bindings (inbound tag → exit slot; a slot of 0 or a missing tag means
// direct). It also keeps one socks outbound per exit, so a rule can never point
// at a missing tag.
func ApplyExitBindings(templateJSON string, exits []ExitSpec, bindings map[string]int) (updated string, changed bool, err error) {
	withOutbounds, _, err := EnsureExitOutbounds(templateJSON, exits)
	if err != nil {
		return templateJSON, false, err
	}
	exits = ExitSpecsFrom(exits)
	valid := make(map[int]bool, len(exits))
	for _, exit := range exits {
		valid[exit.Slot] = true
	}

	grouped := make(map[int][]string)
	for tag, slot := range bindings {
		tag = strings.TrimSpace(tag)
		if tag == "" || slot <= 0 || !valid[slot] {
			continue
		}
		grouped[slot] = append(grouped[slot], tag)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(withOutbounds), &doc); err != nil {
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

	kept := make([]any, 0, len(rules)+len(grouped))
	insertAt := -1
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if exitRuleSlot(rule) > 0 {
			if insertAt < 0 {
				insertAt = len(kept)
			}
			continue
		}
		kept = append(kept, raw)
	}

	slots := make([]int, 0, len(grouped))
	for slot := range grouped {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	built := make([]any, 0, len(slots))
	for _, slot := range slots {
		tags := CleanInboundTags(grouped[slot])
		if len(tags) == 0 {
			continue
		}
		sort.Strings(tags)
		built = append(built, map[string]any{
			"type":        "field",
			"network":     "tcp,udp",
			"inboundTag":  tags,
			"outboundTag": ExitOutboundTag(slot),
			"ruleTag":     ExitRuleTag(slot),
		})
	}
	if insertAt < 0 {
		insertAt = exitInsertIndex(kept)
	}
	if insertAt > len(kept) {
		insertAt = len(kept)
	}
	merged := make([]any, 0, len(kept)+len(built))
	merged = append(merged, kept[:insertAt]...)
	merged = append(merged, built...)
	merged = append(merged, kept[insertAt:]...)

	routing["rules"] = merged
	doc["routing"] = routing
	if equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	return encodeDoc(doc)
}

// ReadExitBindings returns inboundTag → exit slot from the managed rules. An
// inbound that appears in no managed rule is direct and therefore absent.
func ReadExitBindings(templateJSON string) map[string]int {
	bindings := map[string]int{}
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return bindings
	}
	routing, err := routingMap(doc)
	if err != nil {
		return bindings
	}
	rules, err := ruleSlice(routing)
	if err != nil {
		return bindings
	}
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		slot := exitRuleSlot(rule)
		if slot <= 0 {
			continue
		}
		for _, tag := range ruleInboundTags(rule) {
			bindings[tag] = slot
		}
	}
	return bindings
}

// RegisteredExitSlots lists the slots whose outbound is already in the template.
func RegisteredExitSlots(templateJSON string) []int {
	slots := []int{}
	var doc map[string]any
	if err := json.Unmarshal([]byte(templateJSON), &doc); err != nil {
		return slots
	}
	outbounds, err := outboundSlice(doc)
	if err != nil {
		return slots
	}
	for _, entry := range outbounds {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		if slot := ExitTagSlot(tag); slot > 0 {
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	return slots
}

// RemoveExitBinding deletes one inbound's binding and rebuilds the rules. It is
// the pair of ApplyExitBindings for a single claim ("this inbound is direct
// now").
func RemoveExitBinding(templateJSON string, exits []ExitSpec, inboundTag string) (updated string, changed bool, err error) {
	bindings := ReadExitBindings(templateJSON)
	delete(bindings, inboundTag)
	return ApplyExitBindings(templateJSON, exits, bindings)
}

// BindExit claims one inbound for one exit.
func BindExit(templateJSON string, exits []ExitSpec, inboundTag string, slot int) (updated string, changed bool, err error) {
	if slot <= 0 {
		return RemoveExitBinding(templateJSON, exits, inboundTag)
	}
	bindings := ReadExitBindings(templateJSON)
	bindings[inboundTag] = slot
	return ApplyExitBindings(templateJSON, exits, bindings)
}

// RemoveExitOutbound drops one exit's outbound and binding, leaving the inbounds
// that used it on the direct path.
func RemoveExitOutbound(templateJSON string, slot int) (updated string, changed bool, err error) {
	tag := ExitOutboundTag(slot)
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
		if got, _ := m["tag"].(string); got == tag {
			continue
		}
		kept = append(kept, entry)
	}
	doc["outbounds"] = kept

	routing, err := routingMap(doc)
	if err != nil {
		return templateJSON, false, err
	}
	rules, err := ruleSlice(routing)
	if err != nil {
		return templateJSON, false, err
	}
	keptRules := make([]any, 0, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			keptRules = append(keptRules, raw)
			continue
		}
		if exitRuleSlot(rule) == slot {
			continue
		}
		if outbound, _ := rule["outboundTag"].(string); outbound == tag {
			continue
		}
		keptRules = append(keptRules, raw)
	}
	routing["rules"] = keptRules
	doc["routing"] = routing
	if equalJSON(templateJSON, doc) {
		return templateJSON, false, nil
	}
	return encodeDoc(doc)
}

// exitRuleSlot reads the slot from a managed rule's ruleTag (0 when the rule is
// not one of ours).
func exitRuleSlot(rule map[string]any) int {
	tag, _ := rule["ruleTag"].(string)
	if !strings.HasPrefix(tag, ExitRulePrefix) {
		return 0
	}
	slot, err := strconv.Atoi(strings.TrimPrefix(tag, ExitRulePrefix))
	if err != nil || slot <= 0 {
		return 0
	}
	return slot
}

// exitInsertIndex is where the managed exit rules go: ahead of the legacy
// "all inbounds" takeover rule (so a per-inbound choice wins) and ahead of a
// catch-all rule, but after every rule with real criteria, which must keep
// winning.
func exitInsertIndex(rules []any) int {
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if isManagedRule(rule) || isCatchAllRule(rule) {
			return i
		}
	}
	return len(rules)
}

func isRemovedTag(tag string, removed map[string]struct{}) bool {
	_, gone := removed[tag]
	return gone
}

func hasSlot(exits []ExitSpec, slot int) bool {
	for _, exit := range exits {
		if exit.Slot == slot {
			return true
		}
	}
	return false
}

func encodeDoc(doc map[string]any) (string, bool, error) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

// ParseExits decodes the engine's exit list from a /api/exits answer.
func ParseExits(raw []byte) ([]ExitSpec, error) {
	var payload struct {
		Exits []ExitSpec `json:"exits"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.Exits == nil {
		return nil, errors.New("vpngate engine returned no exit list")
	}
	return payload.Exits, nil
}
