package vpngate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrEngineUnavailable is returned when the engine's internal API cannot be
// reached. Callers translate it into a panel error instead of a 500 so the UI
// can show "engine not running" with the supervisor's reason.
var ErrEngineUnavailable = errors.New("vpngate engine is not reachable")

// maxResponseBytes caps one engine response; /api/nodes with a few hundred
// nodes stays far below this, and it stops a wedged engine from streaming
// forever into the panel's memory.
const maxResponseBytes = 8 << 20

// Client talks to the engine's loopback HTTP API, presenting the shared token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for cfg. It never dials anything by itself.
func NewClient(cfg Config) *Client {
	timeout := cfg.APITimeout
	if timeout <= 0 {
		timeout = defaultAPITimeout
	}
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL(), "/"),
		token:   cfg.Token,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				// The engine lives on loopback only; never inherit a proxy from
				// the environment, or a configured HTTP_PROXY would swallow it.
				Proxy:               nil,
				MaxIdleConns:        4,
				IdleConnTimeout:     30 * time.Second,
				DisableCompression:  true,
				ForceAttemptHTTP2:   false,
				TLSHandshakeTimeout: 5 * time.Second,
			},
		},
	}
}

// BaseURL exposes the API root (used by health checks and logging).
func (c *Client) BaseURL() string { return c.baseURL }

// Get performs a GET and returns the raw response body.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// PostJSON performs a POST with an optional JSON body and returns the raw
// response body.
func (c *Client) PostJSON(ctx context.Context, path string, payload any) ([]byte, error) {
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = encoded
	}
	return c.do(ctx, http.MethodPost, path, body)
}

// Post sends a POST with no body.
func (c *Client) Post(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, []byte("{}"))
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	url := c.baseURL + "/" + strings.TrimPrefix(path, "/")
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(tokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEngineUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	data, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrEngineUnavailable, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

// Health probes the engine's lightweight health endpoint.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.Get(ctx, "/api/health")
	return err
}

// Nodes returns the node list plus engine state as the engine shaped it.
func (c *Client) Nodes(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, "/api/nodes")
}

// EngineStatus returns the per-service health report of the engine.
func (c *Client) EngineStatus(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, "/api/gateway_status")
}

// Logs returns today's structured engine log entries.
func (c *Client) Logs(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, "/api/logs")
}

// RefreshNodes starts a background node refresh.
func (c *Client) RefreshNodes(ctx context.Context) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/refresh_nodes", nil)
}

// CheckNodes runs the maintenance pass synchronously.
func (c *Client) CheckNodes(ctx context.Context) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/check", nil)
}

// TestNodes re-tests the given node ids.
func (c *Client) TestNodes(ctx context.Context, ids []string) (json.RawMessage, error) {
	if ids == nil {
		ids = []string{}
	}
	return c.rawPost(ctx, "/api/test_nodes", map[string]any{"ids": ids})
}

// Connect pins the engine to one node.
func (c *Client) Connect(ctx context.Context, id string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/connect", map[string]any{"id": id})
}

// Disconnect tears the tunnel down and disables auto-reconnect.
func (c *Client) Disconnect(ctx context.Context) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/disconnect", nil)
}

// Exits returns every exit slot plus the host's memory budget.
func (c *Client) Exits(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, "/api/exits")
}

// CreateExit adds one exit slot. The engine picks the device, policy table and
// loopback proxy port; nothing is connected until the operator asks.
func (c *Client) CreateExit(ctx context.Context, name string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/exits/create", map[string]any{"name": name})
}

// UpdateExit renames an exit and/or changes its routing policy.
func (c *Client) UpdateExit(ctx context.Context, exitID string, payload map[string]any) (json.RawMessage, error) {
	body := map[string]any{"id": exitID}
	for key, value := range payload {
		body[key] = value
	}
	return c.rawPost(ctx, "/api/exits/update", body)
}

// DeleteExit removes an exit slot (the default exit cannot be deleted).
func (c *Client) DeleteExit(ctx context.Context, exitID string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/exits/delete", map[string]any{"id": exitID})
}

// ConnectExit puts one exit on one node. Switching is the same operation: the
// engine replaces that slot's tunnel and leaves the other exits alone.
func (c *Client) ConnectExit(ctx context.Context, exitID, nodeID string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/exits/connect", map[string]any{"exit": exitID, "node": nodeID})
}

// AutoConnectExit lets the engine pick a node its saved policy accepts.
func (c *Client) AutoConnectExit(ctx context.Context, exitID string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/exits/auto-connect", map[string]any{"exit": exitID})
}

// DisconnectExit tears one exit's tunnel down and disables its auto-connect.
func (c *Client) DisconnectExit(ctx context.Context, exitID string) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/exits/disconnect", map[string]any{"exit": exitID})
}

// SetRouting switches the engine's routing mode without touching the tunnel
// port, so Xray keeps routing through the same local proxy.
func (c *Client) SetRouting(ctx context.Context, mode, country, ipType string, favFallback bool) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/update_routing", map[string]any{
		"routing_mode":      mode,
		"force_country":     country,
		"routing_ip_type":   ipType,
		"fav_fail_fallback": favFallback,
	})
}

// TestProxy measures the live exit through the local proxy.
func (c *Client) TestProxy(ctx context.Context) (json.RawMessage, error) {
	return c.rawPost(ctx, "/api/test_proxy", nil)
}

func (c *Client) raw(ctx context.Context, path string) (json.RawMessage, error) {
	data, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (c *Client) rawPost(ctx context.Context, path string, payload any) (json.RawMessage, error) {
	data, err := c.PostJSON(ctx, path, payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// APIError is a non-2xx answer from the engine.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("vpngate engine returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("vpngate engine returned HTTP %d: %s", e.Status, e.Body)
}

// StatusCode reports the HTTP status the engine answered with (0 when the
// error never reached the engine).
func (e *APIError) StatusCode() int { return e.Status }
