// Package release reads LxGate releases from GitHub. It is deliberately small
// and dependency-free: the panel uses it for the in-app version badge and the
// changelog shown on the System & Update page.
//
// LxGate is published from a public repository, so checking for updates and
// downloading one need no credential at all. A token is optional: it raises
// GitHub's anonymous API limit (60 requests an hour per address, which a panel
// checking every 15 minutes shares with anything else on the host) and lets a
// panel follow a private fork. When one is set it comes from the panel settings
// (or GITHUB_TOKEN/GITHUB_API_TOKEN in the environment) and is never logged.
// The panel itself reuses a recent answer for a few minutes (see
// SystemController), so a reloaded page does not spend the limit.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout bounds one GitHub API call.
const DefaultTimeout = 12 * time.Second

// maxBodyBytes caps a release payload; a release body is large but never this
// large, and the cap keeps a misbehaving proxy from exhausting memory.
const maxBodyBytes = 2 << 20

// The two kinds of release the panel can end up reading. The panel no longer
// asks the operator to choose one: LatestAvailable tries the newest stable
// release and falls back to the rolling dev-latest pre-release when the
// repository has none. The names stay because the resolved channel is reported
// to the UI and older settings still store one.
const (
	ChannelStable = "stable"
	ChannelDev    = "dev"
	// DevTag is the fixed tag of the rolling pre-release the CI force-moves on
	// every main commit.
	DevTag = "dev-latest"
)

// NormalizeChannel maps operator input onto a known channel, defaulting to
// stable for anything unrecognised so a typo never silently switches a panel
// onto the dev channel.
func NormalizeChannel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ChannelDev, DevTag, "development", "preview", "beta":
		return ChannelDev
	default:
		return ChannelStable
	}
}

// Info is the subset of a GitHub release the panel needs.
type Info struct {
	TagName     string    `json:"tagName"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"htmlUrl"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	// TargetCommitish is the branch or SHA the tag points at; a dev release
	// grepped from its notes is preferred, this is the fallback.
	TargetCommitish string `json:"targetCommitish,omitempty"`
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
// GITHUB_API_TOKEN in the environment is used until the caller sets the token
// stored in the panel settings.
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

// SetToken swaps the credential used for the next calls. An empty value falls
// back to the environment token from NewClient.
func (c *Client) SetToken(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		token = firstNonEmptyEnv("GITHUB_TOKEN", "GITHUB_API_TOKEN")
	}
	c.token = token
}

// HasToken reports whether a credential is currently configured, never its value.
func (c *Client) HasToken() bool { return c.token != "" }

// WithToken returns a copy of the client that authenticates with token, leaving
// the receiver untouched. The controller uses it so two requests handled at the
// same time cannot race on one client's credential.
func (c *Client) WithToken(token string) *Client {
	clone := *c
	clone.SetToken(token)
	return &clone
}

// LatestAvailable fetches the release to offer the panel, and which channel it
// came from. It asks for the newest stable release first; when the repository
// has published none, GitHub answers 404 and the rolling dev-latest pre-release
// is used instead, so a repository that only ships main builds still offers an
// update rather than reporting "no release". A 404 from both endpoints means
// nothing has been published at all, and that is reported as such.
func (c *Client) LatestAvailable(ctx context.Context) (*Info, string, error) {
	info, err := c.Latest(ctx)
	if err == nil {
		return info, ChannelStable, nil
	}
	if !errors.Is(err, ErrNoRelease) {
		return nil, "", err
	}
	devInfo, devErr := c.ByTag(ctx, DevTag)
	if devErr == nil {
		return devInfo, ChannelDev, nil
	}
	if errors.Is(devErr, ErrNoRelease) {
		// Neither endpoint has anything: keep the stable wording, which is what
		// an operator publishing their first release will fix.
		return nil, "", err
	}
	return nil, "", devErr
}

// Latest fetches the newest published, non-prerelease release.
func (c *Client) Latest(ctx context.Context) (*Info, error) {
	return c.fetch(ctx, "/releases/latest")
}

// ByTag fetches one release by its tag, including a pre-release such as
// dev-latest (the `/releases/latest` endpoint ignores pre-releases).
func (c *Client) ByTag(ctx context.Context, tag string) (*Info, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, fmt.Errorf("release tag is empty")
	}
	return c.fetch(ctx, "/releases/tags/"+tag)
}

// ErrNoRelease means the repository exists but has no published release yet.
// GitHub also answers 404 for a private fork read without a token, so the
// message says both; a public repository needs no token to avoid it.
var ErrNoRelease = errors.New("no published release found")

// ErrRateLimited means GitHub refused the call for hitting its limit: 60
// requests an hour without a token, counted per source address. It wraps
// nothing, because a check that returns it is not "the repository is fine, it
// just has no release".
var ErrRateLimited = errors.New("GitHub API rate limit reached")

// ErrTokenRejected means GitHub answered 401/403 for a token that is set,
// rather than for the anonymous limit: the credential is invalid, expired, or
// not allowed to read the repository.
var ErrTokenRejected = errors.New("GitHub rejected the token")

func (c *Client) fetch(ctx context.Context, path string) (*Info, error) {
	if c.repo == "" || !strings.Contains(c.repo, "/") {
		return nil, fmt.Errorf("invalid release repository %q", c.repo)
	}
	url := fmt.Sprintf("%s/repos/%s%s", c.baseURL, c.repo, path)
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
		return nil, c.notFoundError()
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w (HTTP 401): check that it is valid and still allowed to read the release repository", ErrTokenRejected)
	}
	if resp.StatusCode == http.StatusForbidden {
		// A 403 is the rate limit when GitHub says nothing is left, and a bad
		// token when one is set and the limit is not the reason. Without a token
		// there is no credential to blame: an anonymous 403 is the limit.
		if limited, reset := rateLimited(resp); limited {
			return nil, rateLimitError(reset)
		}
		if c.token != "" {
			return nil, fmt.Errorf("%w (HTTP 403): check that it is valid and allowed to read the release repository", ErrTokenRejected)
		}
		return nil, rateLimitError(time.Time{})
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName         string `json:"tag_name"`
		Name            string `json:"name"`
		Body            string `json:"body"`
		HTMLURL         string `json:"html_url"`
		PublishedAt     string `json:"published_at"`
		Prerelease      bool   `json:"prerelease"`
		Draft           bool   `json:"draft"`
		TargetCommitish string `json:"target_commitish"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	info := &Info{
		TagName:         strings.TrimSpace(payload.TagName),
		Name:            payload.Name,
		Body:            payload.Body,
		HTMLURL:         payload.HTMLURL,
		Prerelease:      payload.Prerelease,
		Draft:           payload.Draft,
		TargetCommitish: strings.TrimSpace(payload.TargetCommitish),
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

// notFoundError distinguishes the two meanings of a 404 from GitHub: a public
// repository with no published release, and a private fork read without a
// token (GitHub hides its existence behind the same 404). Neither is a reason
// to demand a token for this public repository.
func (c *Client) notFoundError() error {
	if c.token != "" {
		return ErrNoRelease
	}
	return fmt.Errorf("%w: the release repository answered 404 -- it has no published release, or it is a private fork that needs a read-only token", ErrNoRelease)
}

// rateLimited reads GitHub's rate-limit headers off a 403. A limited call always
// carries X-RateLimit-Remaining: 0; the reset stamp is optional.
func rateLimited(resp *http.Response) (bool, time.Time) {
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		return false, time.Time{}
	}
	var reset time.Time
	if raw := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			reset = time.Unix(seconds, 0)
		}
	}
	return true, reset
}

// rateLimitError explains the anonymous limit and what raises it. The reset
// time is omitted when GitHub did not send one. The panel keeps a recent answer
// for a few minutes, so in practice the message clears itself.
func rateLimitError(reset time.Time) error {
	if reset.IsZero() {
		return fmt.Errorf("%w: this server has used up GitHub's anonymous limit (60 calls an hour); the panel keeps its last answer for a few minutes, so this clears up on its own, or try again later", ErrRateLimited)
	}
	return fmt.Errorf("%w: this server has used up GitHub's anonymous limit (60 calls an hour), which resets at %s UTC; the panel keeps its last answer for a few minutes", ErrRateLimited, reset.UTC().Format("15:04"))
}

// NormalizeTag strips a leading "v" so "v1.2.3" and "1.2.3" compare equal.
func NormalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// commitPattern matches the `commit=<sha>` marker the release workflow writes
// into a dev build's notes.
var commitPattern = regexp.MustCompile(`(?i)commit=([0-9a-f]{7,40})`)

// ExtractCommit reads the build commit recorded in a dev release: first the
// `commit=<sha>` marker in its notes, falling back to the target commit when
// that is a bare SHA.
func ExtractCommit(info *Info) string {
	if info == nil {
		return ""
	}
	if m := commitPattern.FindStringSubmatch(info.Body); m != nil {
		return strings.ToLower(m[1])
	}
	candidate := strings.TrimSpace(info.TargetCommitish)
	if IsCommitSHA(candidate) {
		return strings.ToLower(candidate)
	}
	return ""
}

// IsCommitSHA reports whether value looks like a git object name.
func IsCommitSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 7 || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// ShortCommit trims a SHA to the eight characters the panel displays.
func ShortCommit(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// CommitsEqual compares a short (build-stamped) commit against a full release
// commit by prefix, so an 8-character build stamp matches a 40-character SHA.
// An empty side never matches: a build that does not know its commit cannot be
// assumed to be the release's.
func CommitsEqual(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
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
