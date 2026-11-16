// Package controllertest exercises controller endpoints over their real HTTP
// wiring. It lives outside internal/web/controller on purpose: the controller
// package carries a very large test suite, and a self-contained package keeps
// this integration test cheap to build and run on its own.
package controllertest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/web/controller"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/vpngate"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

const takeoverTestTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}],
  "routing": {"rules": [
    {"type": "field", "ip": ["geoip:private"], "outboundTag": "direct"},
    {"type": "field", "network": "tcp,udp", "outboundTag": "direct"}
  ]}
}`

// newTakeoverTestEngine wires the real VPNGateController over a fresh database
// with one logged-in user and a template that has a private-IP rule plus a
// catch-all, which is what the takeover rule has to fit between.
func newTakeoverTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "lxgate.db"))

	if err := database.GetDB().Create(&model.User{Username: "takeover", Password: "x"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, row := range []model.Inbound{
		{UserId: 1, Tag: "in-a", Port: 12001, Protocol: model.VLESS, Enable: true, Remark: "A"},
		{UserId: 1, Tag: "in-b", Port: 12002, Protocol: model.Shadowsocks, Enable: true, Remark: "B"},
	} {
		inbound := row
		if err := database.GetDB().Create(&inbound).Error; err != nil {
			t.Fatalf("seed inbound %s: %v", row.Tag, err)
		}
	}
	setting := &service.XraySettingService{}
	if err := setting.SaveXraySetting(takeoverTestTemplate); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	engine := gin.New()
	store := cookie.NewStore([]byte("takeover-test-secret"))
	engine.Use(sessions.Sessions("lxgate", store))
	engine.GET("/test-login", func(c *gin.Context) {
		if err := session.SetLoginUser(c, &model.User{Id: 1, Username: "takeover"}); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	api := engine.Group("/panel/api")
	controller.NewVPNGateController(api.Group("/vpngate"))
	return engine
}

type takeoverResponse struct {
	Success bool `json:"success"`
	Obj     struct {
		Mode        string   `json:"mode"`
		InboundTags []string `json:"inboundTags"`
		Bound       bool     `json:"bound"`
		RuleIndex   int      `json:"ruleIndex"`
		Changed     bool     `json:"changed"`
		Inbounds    []struct {
			Tag string `json:"tag"`
		} `json:"inbounds"`
	} `json:"obj"`
}

func currentTemplate(t *testing.T) string {
	t.Helper()
	setting := &service.XraySettingService{}
	template, err := setting.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	return template
}

func routingRules(t *testing.T, template string) []map[string]any {
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

func TestTakeoverEndpointAllThenSelectedThenManual(t *testing.T) {
	engine := newTakeoverTestEngine(t)

	// Login over the cookie jar the engine shares with the test client.
	login := httptest.NewRecorder()
	engine.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/test-login", nil))
	cookies := login.Result().Cookies()

	call := func(method, url string, body any) takeoverResponse {
		var reader = bytes.NewReader(nil)
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, url, reader)
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d, body=%s", method, url, rec.Code, rec.Body.String())
		}
		var parsed takeoverResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
		if !parsed.Success {
			t.Fatalf("%s %s failed: %s", method, url, rec.Body.String())
		}
		return parsed
	}

	// A fresh template reads as manual with both inbounds offered.
	initial := call(http.MethodGet, "/panel/api/vpngate/takeover", nil)
	if initial.Obj.Mode != "manual" {
		t.Fatalf("initial mode = %s", initial.Obj.Mode)
	}
	if len(initial.Obj.Inbounds) != 2 {
		t.Fatalf("inbounds offered = %d, want 2", len(initial.Obj.Inbounds))
	}

	all := call(http.MethodPost, "/panel/api/vpngate/takeover", map[string]any{"mode": "all"})
	if all.Obj.Mode != "all" || !all.Obj.Bound {
		t.Fatalf("all state = %+v", all.Obj)
	}
	state, err := vpngate.ReadTakeover(currentTemplate(t))
	if err != nil {
		t.Fatalf("read takeover: %v", err)
	}
	if state.Mode != vpngate.TakeoverModeAll {
		t.Fatalf("template mode = %s", state.Mode)
	}
	// SaveXraySetting hoists a stats/API rule of its own, so assert the ordering
	// the panel promises rather than absolute positions: the managed rule sits
	// after the operator's rules and immediately before the catch-all.
	rules := routingRules(t, currentTemplate(t))
	managedAt := -1
	catchAllAt := -1
	privateAt := -1
	for i, rule := range rules {
		tag, _ := rule["ruleTag"].(string)
		if tag == vpngate.TakeoverRuleTag {
			managedAt = i
		}
		if ips, ok := rule["ip"].([]any); ok && len(ips) == 1 && ips[0] == "geoip:private" {
			privateAt = i
		}
		if rule["outboundTag"] == "direct" {
			if _, hasInbound := rule["inboundTag"]; !hasInbound {
				if _, hasIP := rule["ip"]; !hasIP {
					if _, hasDomain := rule["domain"]; !hasDomain {
						catchAllAt = i
					}
				}
			}
		}
	}
	if managedAt < 0 {
		t.Fatalf("managed rule missing; rules=%v", rules)
	}
	if managedAt != state.RuleIndex {
		t.Fatalf("managed rule index = %d, controller reported %d", managedAt, state.RuleIndex)
	}
	if privateAt < 0 || managedAt <= privateAt {
		t.Fatalf("managed rule at %d must come after the private-IP rule at %d", managedAt, privateAt)
	}
	if catchAllAt < 0 || managedAt >= catchAllAt {
		t.Fatalf("managed rule at %d must come before the catch-all at %d", managedAt, catchAllAt)
	}

	selected := call(http.MethodPost, "/panel/api/vpngate/takeover", map[string]any{
		"mode":        "selected",
		"inboundTags": []string{"in-b", "not-an-inbound"},
	})
	if selected.Obj.Mode != "selected" {
		t.Fatalf("selected mode = %s", selected.Obj.Mode)
	}
	if len(selected.Obj.InboundTags) != 1 || selected.Obj.InboundTags[0] != "in-b" {
		t.Fatalf("selected tags = %v, want only the inbound that exists", selected.Obj.InboundTags)
	}
	state, _ = vpngate.ReadTakeover(currentTemplate(t))
	if len(state.InboundTags) != 1 || state.InboundTags[0] != "in-b" {
		t.Fatalf("template tags = %v", state.InboundTags)
	}

	manual := call(http.MethodPost, "/panel/api/vpngate/takeover", map[string]any{"mode": "manual"})
	if manual.Obj.Mode != "manual" {
		t.Fatalf("manual mode = %s", manual.Obj.Mode)
	}
	state, _ = vpngate.ReadTakeover(currentTemplate(t))
	if state.RuleIndex != -1 {
		t.Fatalf("manual mode left a managed rule at %d", state.RuleIndex)
	}
	if !vpngate.HasOutbound(currentTemplate(t), vpngate.OutboundTag) {
		t.Fatal("manual mode must keep the outbound a manual rule may point at")
	}

	// Unknown modes are rejected without touching the template.
	before := currentTemplate(t)
	req := httptest.NewRequest(http.MethodPost, "/panel/api/vpngate/takeover", bytes.NewBufferString(`{"mode":"sometimes"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	var rejected takeoverResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if rejected.Success {
		t.Fatal("unknown mode must be rejected")
	}
	if currentTemplate(t) != before {
		t.Fatal("a rejected request must not touch the template")
	}
}
