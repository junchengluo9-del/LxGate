package release

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if _, err := client.Latest(context.Background()); err != ErrNoRelease {
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

func TestNormalizeTag(t *testing.T) {
	if got := NormalizeTag(" v1.2.3 "); got != "1.2.3" {
		t.Fatalf("normalize = %q", got)
	}
	if got := NormalizeTag("dev-latest"); got != "dev-latest" {
		t.Fatalf("normalize = %q", got)
	}
}
