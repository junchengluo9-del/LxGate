package vpngate

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := Config{
		Enabled:    true,
		APIPort:    18787,
		ProxyPort:  7928,
		Token:      "test-token-0123456789",
		APITimeout: 5 * time.Second,
	}
	client := NewClient(cfg)
	// Point the client at the fixture server: BaseURL is derived from the config,
	// so tests rewrite it rather than punching a hole in production code.
	client.baseURL = server.URL
	return client, server
}

func TestClientSendsTokenAndDecodesJSON(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get(tokenHeader); got != "test-token-0123456789" {
			t.Errorf("token header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[{"id":"n1","ping":12}],"state":{"active_openvpn_node_id":"n1"}}`))
	})

	raw, err := client.Nodes(context.Background())
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	var payload struct {
		Nodes []struct {
			ID   string `json:"id"`
			Ping int    `json:"ping"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Nodes) != 1 || payload.Nodes[0].ID != "n1" {
		t.Fatalf("unexpected payload: %s", raw)
	}
}

func TestClientPostSendsJSONBody(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["id"] != "node-7" {
			t.Errorf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	if _, err := client.Connect(context.Background(), "node-7"); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func TestClientMapsHTTPError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
	})

	_, err := client.Nodes(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status = %d", apiErr.StatusCode())
	}
	if !strings.Contains(apiErr.Error(), "Unauthorized") {
		t.Fatalf("message lost: %v", apiErr)
	}
}

func TestClientReportsEngineUnavailable(t *testing.T) {
	// Reserve a port, close it, and point the client at it: a refused connection
	// must surface as ErrEngineUnavailable, never as a generic failure.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	cfg := Config{Enabled: true, Token: "t", APITimeout: time.Second}
	client := NewClient(cfg)
	client.baseURL = "http://" + addr

	if _, err := client.Nodes(context.Background()); !errors.Is(err, ErrEngineUnavailable) {
		t.Fatalf("want ErrEngineUnavailable, got %v", err)
	}
}

func TestClientHealth(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"embedded":true}`))
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestClientSetRoutingPayload(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["routing_mode"] != "fixed_region" || body["force_country"] != "JP" {
			t.Fatalf("payload = %#v", body)
		}
		if body["fav_fail_fallback"] != true {
			t.Fatalf("fav_fail_fallback = %#v", body["fav_fail_fallback"])
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	if _, err := client.SetRouting(context.Background(), "fixed_region", "JP", "all", true); err != nil {
		t.Fatalf("set routing: %v", err)
	}
}
