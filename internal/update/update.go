// Package update asks GitHub for vlt's latest release and compares it with
// the running version. It only reads: nothing is downloaded or installed.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ReleasesURL is the page listing vlt's releases.
const ReleasesURL = "https://github.com/heyjdp/vlt/releases"

// latestAPI is GitHub's endpoint for the newest published release (drafts
// and pre-releases are never returned).
const latestAPI = "https://api.github.com/repos/heyjdp/vlt/releases/latest"

// Limits on the request and on what is read of the response.
const (
	requestTimeout = 15 * time.Second
	maxResponse    = 1 << 20
	maxNotes       = 2000 // runes of release notes kept
)

// Errors a check can return, besides network errors.
var (
	// ErrNoRelease means the project has not published a release yet.
	ErrNoRelease = errors.New("no release has been published yet")
	// ErrRateLimited means GitHub refused the request for now (the
	// unauthenticated API allows 60 requests an hour per address).
	ErrRateLimited = errors.New("GitHub's request limit was reached; try again later")
)

// Release is a published release of vlt.
type Release struct {
	Tag   string // vX.Y.Z
	Notes string // the release notes, shortened to maxNotes runes
}

// PageURL is the release's page on GitHub. It is built from the validated
// tag, never taken from the response, so it always points at vlt's own
// release page.
func (r Release) PageURL() string { return ReleasesURL + "/tag/" + r.Tag }

// Checker fetches the latest release.
type Checker struct {
	client *http.Client
	url    string
}

// NewChecker returns a Checker that asks GitHub. It honours the usual proxy
// environment variables (HTTPS_PROXY, NO_PROXY), and follows a redirect
// only over HTTPS to api.github.com.
func NewChecker() *Checker {
	return &Checker{client: &http.Client{Timeout: requestTimeout, CheckRedirect: githubOnly}, url: latestAPI}
}

// apiHost is the only host a check may be redirected to.
const apiHost = "api.github.com"

// githubOnly refuses a redirect anywhere but HTTPS on apiHost.
func githubOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" || req.URL.Host != apiHost {
		return fmt.Errorf("refusing a redirect to %s", req.URL.Redacted())
	}
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	return nil
}

// Latest returns the newest published release.
func (c *Checker) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, http.NoBody)
	if err != nil {
		return Release{}, fmt.Errorf("update.Latest: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub requires a User-Agent; it names the app, not its version.
	req.Header.Set("User-Agent", "vlt-update-check")

	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("update.Latest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Release{}, fmt.Errorf("update.Latest: %w", ErrNoRelease)
	case http.StatusTooManyRequests:
		return Release{}, fmt.Errorf("update.Latest: %w", ErrRateLimited)
	case http.StatusForbidden:
		// GitHub also answers 403 when the limit is reached; a proxy or a
		// block answers it for other reasons.
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return Release{}, fmt.Errorf("update.Latest: %w", ErrRateLimited)
		}
		return Release{}, fmt.Errorf("update.Latest: GitHub answered %s", resp.Status)
	default:
		return Release{}, fmt.Errorf("update.Latest: GitHub answered %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("update.Latest: parse the answer: %w", err)
	}
	if _, ok := parseRelease(body.TagName); !ok {
		return Release{}, fmt.Errorf("update.Latest: unexpected release tag %q", body.TagName)
	}
	return Release{Tag: body.TagName, Notes: shorten(printable(body.Body), maxNotes)}, nil
}

// printable drops control and format characters (such as bidirectional
// overrides, which could make the notes read differently from what they
// say), keeping newlines and tabs.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.In(r, unicode.Cc, unicode.Cf) {
			return r
		}
		return -1
	}, s)
}

// shorten trims s and cuts it to at most n runes, marking a cut with "…".
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// semver is a release version, major.minor.patch.
type semver [3]int

// less reports whether v is older than w.
func (v semver) less(w semver) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

var (
	releaseTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	// sinceTag is the part git describe adds after the tag for a later
	// commit: the number of commits since it and the abbreviated hash.
	sinceTag = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)
	// versionTag is a tag, optionally with a pre-release suffix (v0.2.0-rc1).
	versionTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)
)

// parseRelease parses a release tag, vX.Y.Z.
func parseRelease(tag string) (semver, bool) {
	m := releaseTag.FindStringSubmatch(tag)
	if m == nil {
		return semver{}, false
	}
	return toSemver(m[1:4])
}

// toSemver converts three decimal strings.
func toSemver(parts []string) (semver, bool) {
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

// IsNewer reports whether the release tagged latest is newer than the
// running version current, as git describe names it (v0.1.0, v0.1.0-dirty,
// v0.1.0-5-gabc1234, v0.2.0-rc1). A build after v0.1.0 is not yet v0.1.1,
// so v0.1.1 is newer than v0.1.0-5-gabc1234; a pre-release of v0.2.0 is
// older than v0.2.0 itself. ok is false when either cannot be compared, as
// for a "dev" build or one made without tags.
func IsNewer(current, latest string) (newer, ok bool) {
	l, ok := parseRelease(latest)
	if !ok {
		return false, false
	}
	current = strings.TrimSuffix(current, "-dirty")
	current = sinceTag.ReplaceAllString(current, "")
	m := versionTag.FindStringSubmatch(current)
	if m == nil {
		return false, false
	}
	c, ok := toSemver(m[1:4])
	if !ok {
		return false, false
	}
	if m[4] != "" { // a pre-release comes before its release
		return !l.less(c), true
	}
	return c.less(l), true
}
