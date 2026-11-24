package release

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestParsesRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/junchengluo9-del/LxGate/releases/latest" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("GitHub requires a User-Agent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v1.4.0",
			"name":         "LxGate 1.4.0",
			"body":         "- added VPNGate exit page",
			"html_url":     "https://github.com/junchengluo9-del/LxGate/releases/tag/v1.4.0",
			"published_at": "2026-11-01T10:00:00Z",
		})
	}))
	defer server.Close()

	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)

	info, err := client.Latest(context.Background())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if info.TagName != "v1.4.0" {
		t.Fatalf("tag = %q", info.TagName)
	}
	if info.Body == "" || info.HTMLURL == "" {
		t.Fatalf("changelog/url missing: %#v", info)
	}
	if info.PublishedAt.IsZero() {
		t.Fatal("published date not parsed")
	}
}

func TestLatestMissingRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)
	client.SetToken("ghp_example")
	if _, err := client.Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("want ErrNoRelease, got %v", err)
	}
}

func TestLatestRejectsBadRepo(t *testing.T) {
	client := NewClient("not-a-repo")
	client.SetBaseURL("http://127.0.0.1:1")
	if _, err := client.Latest(context.Background()); err == nil {
		t.Fatal("a malformed repository must be rejected before any request")
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v1.0.7", "1.0.6", true},
		{"1.0.6", "1.0.6", false},
		{"v1.0.6", "1.0.7", false},
		{"1.2.0", "1.10.0", false},
		{"2.0", "1.9.9", true},
		{"1.0.6", "dev+abc1234", true},
		{"", "1.0.6", false},
		{"garbage", "1.0.6", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestReleaseChannelSelectsEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "dev-latest",
			"name":         "Dev build",
			"prerelease":   true,
			"published_at": "2026-11-17T03:12:11Z",
		})
	}))
	defer server.Close()

	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)

	if _, err := client.Release(context.Background(), "stable"); err != nil {
		t.Fatalf("stable: %v", err)
	}
	dev, err := client.Release(context.Background(), "dev")
	if err != nil {
		t.Fatalf("dev: %v", err)
	}
	if !dev.Prerelease {
		t.Fatal("the dev release must keep its prerelease flag")
	}
	want := []string{
		"/repos/junchengluo9-del/LxGate/releases/latest",
		"/repos/junchengluo9-del/LxGate/releases/tags/dev-latest",
	}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}

func TestReleaseSendsConfiguredToken(t *testing.T) {
	var gotAuth, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3"})
	}))
	defer server.Close()

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")
	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)
	client.SetToken("ghp_example")
	if !client.HasToken() {
		t.Fatal("a configured token must be reported as present")
	}
	if _, err := client.Latest(context.Background()); err != nil {
		t.Fatalf("latest: %v", err)
	}
	if gotAuth != "Bearer ghp_example" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept == "" {
		t.Fatal("the GitHub API version header pair must be sent")
	}
	if client.WithToken("").HasToken() {
		t.Fatal("the copy must not keep the token after clearing it")
	}
	if !strings.Contains(gotAccept, "json") {
		t.Fatalf("Accept = %q", gotAccept)
	}
}

func TestReleaseWithoutTokenExplainsMissingRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")
	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)
	_, err := client.Latest(context.Background())
	if err == nil {
		t.Fatal("a 404 without a token must be reported")
	}
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTokenRejected) {
		t.Fatalf("a 404 is neither a limit nor a rejected token: %v", err)
	}
	// The public repository needs no token: the message may mention a private
	// fork, but it must not tell the operator a token is required.
	if !strings.Contains(err.Error(), "private fork") {
		t.Fatalf("err = %v, want the private-fork caveat", err)
	}
}

func TestReleaseWithoutTokenSurvivesAnonymousRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1764000000")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_TOKEN", "")
	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)
	_, err := client.Latest(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if errors.Is(err, ErrTokenRejected) {
		t.Fatal("an anonymous limit must not be reported as a token problem")
	}
	if !strings.Contains(err.Error(), "UTC") {
		t.Fatalf("err = %v, want the reset time", err)
	}
}

func TestReleaseRejectedTokenIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient("junchengluo9-del/LxGate")
	client.SetBaseURL(server.URL)
	client.SetToken("bad")
	_, err := client.Latest(context.Background())
	if err == nil || !errors.Is(err, ErrTokenRejected) || !strings.Contains(err.Error(), "rejected the token") {
		t.Fatalf("err = %v, want a rejected-token error", err)
	}
}

// A 403 with a token is still the token's fault only when the limit is not the
// reason; a limited call reports the limit.
func TestReleaseForbiddenWithToken(t *testing.T) {
	cases := []struct {
		name      string
		remaining string
		want      error
	}{
		{"limit", "0", ErrRateLimited},
		{"token", "42", ErrTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", tc.remaining)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()

			client := NewClient("junchengluo9-del/LxGate")
			client.SetBaseURL(server.URL)
			client.SetToken("ghp_example")
			_, err := client.Latest(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeChannel(t *testing.T) {
	cases := map[string]string{
		"":              ChannelStable,
		"stable":        ChannelStable,
		"STABLE":        ChannelStable,
		"dev":           ChannelDev,
		" Dev ":         ChannelDev,
		"dev-latest":    ChannelDev,
		"somethingElse": ChannelStable,
	}
	for input, want := range cases {
		if got := NormalizeChannel(input); got != want {
			t.Errorf("NormalizeChannel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeTag(t *testing.T) {
	if got := NormalizeTag(" v1.2.3 "); got != "1.2.3" {
		t.Fatalf("normalize = %q", got)
	}
	if got := NormalizeTag("dev-latest"); got != "dev-latest" {
		t.Fatalf("normalize = %q", got)
	}
}
