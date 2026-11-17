// Package release reads LxGate releases from GitHub. It is deliberately small
// and dependency-free: the panel uses it for the in-app version badge and the
// changelog shown on the System & Update page.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultTimeout bounds one GitHub API call.
const DefaultTimeout = 12 * time.Second

// maxBodyBytes caps a release payload; a release body is large but never this
// large, and the cap keeps a misbehaving proxy from exhausting memory.
const maxBodyBytes = 2 << 20

// Info is the subset of a GitHub release the panel needs.
type Info struct {
	TagName     string    `json:"tagName"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"htmlUrl"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// Client queries one repository's releases.
type Client struct {
	repo       string
	httpClient *http.Client
	baseURL    string
	token      string
	// userAgent is required by the GitHub API.
	userAgent string
}

// NewClient builds a client for "owner/name". An optional GITHUB_TOKEN or
// GITHUB_API_TOKEN in the environment is used to avoid the anonymous rate
// limit; the token is never logged.
func NewClient(repo string) *Client {
	return &Client{
		repo:       strings.TrimSpace(repo),
		httpClient: &http.Client{Timeout: DefaultTimeout},
		baseURL:    "https://api.github.com",
		token:      firstNonEmptyEnv("GITHUB_TOKEN", "GITHUB_API_TOKEN"),
		userAgent:  "LxGate-Panel",
	}
}

// SetBaseURL overrides the API root (tests point it at an httptest server).
func (c *Client) SetBaseURL(url string) { c.baseURL = strings.TrimSuffix(url, "/") }

// Latest fetches the newest published, non-prerelease release.
func (c *Client) Latest(ctx context.Context) (*Info, error) {
	if c.repo == "" || !strings.Contains(c.repo, "/") {
		return nil, fmt.Errorf("invalid release repository %q", c.repo)
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.baseURL, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	info := &Info{
		TagName:    strings.TrimSpace(payload.TagName),
		Name:       payload.Name,
		Body:       payload.Body,
		HTMLURL:    payload.HTMLURL,
		Prerelease: payload.Prerelease,
		Draft:      payload.Draft,
	}
	if payload.PublishedAt != "" {
		if ts, err := time.Parse(time.RFC3339, payload.PublishedAt); err == nil {
			info.PublishedAt = ts
		}
	}
	if info.TagName == "" {
		return nil, errors.New("release payload has no tag")
	}
	return info, nil
}

// ErrNoRelease means the repository exists but has no published release yet.
var ErrNoRelease = errors.New("no published release found")

// NormalizeTag strips a leading "v" so "v1.2.3" and "1.2.3" compare equal.
func NormalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// IsNewer reports whether candidate is a strictly newer version than current.
// Both are compared as dot-separated numeric segments; a "dev+<sha>" build is
// always considered older than a matching stable tag.
func IsNewer(candidate, current string) bool {
	a, okA := parseVersion(NormalizeTag(candidate))
	b, okB := parseVersion(NormalizeTag(current))
	if !okA || !okB {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		var left, right int
		if i < len(a) {
			left = a[i]
		}
		if i < len(b) {
			right = b[i]
		}
		if left != right {
			return left > right
		}
	}
	return false
}

func parseVersion(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	if strings.HasPrefix(value, "dev+") || value == "dev" {
		return []int{0}, true
	}
	// Drop a pre-release/build suffix: "1.2.3-rc1" compares as 1.2.3.
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		value = value[:idx]
	}
	parts := strings.Split(value, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		number := 0
		if part != "" {
			for _, r := range part {
				if r < '0' || r > '9' {
					return nil, false
				}
			}
			for _, r := range part {
				number = number*10 + int(r-'0')
			}
		}
		out = append(out, number)
	}
	return out, len(out) > 0
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
