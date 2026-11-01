package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIsNewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		current, latest string
		newer, ok       bool
	}{
		{"v0.1.0", "v0.1.1", true, true},
		{"v0.1.0", "v0.1.0", false, true},
		{"v0.1.1", "v0.1.0", false, true},
		{"v0.1.9", "v0.2.0", true, true},
		{"v0.9.0", "v1.0.0", true, true},
		{"v0.10.0", "v0.9.0", false, true}, // numeric, not string, order
		{"v0.1.0-dirty", "v0.1.1", true, true},
		{"v0.1.0-dirty", "v0.1.0", false, true},
		{"v0.1.0-5-gabc1234", "v0.1.0", false, true},
		{"v0.1.0-5-gabc1234", "v0.1.1", true, true},
		{"v0.1.0-5-gabc1234-dirty", "v0.1.1", true, true},
		{"v0.2.0-rc1", "v0.2.0", true, true},
		{"v0.2.0-rc1", "v0.1.9", false, true},
		{"v0.2.0-rc1-3-gabc1234", "v0.2.0", true, true},
		{"dev", "v0.1.0", false, false},
		{"abc1234", "v0.1.0", false, false},
		{"abc1234-dirty", "v0.1.0", false, false},
		{"", "v0.1.0", false, false},
		{"v0.1.0", "v0.2.0-rc1", false, false}, // not a release tag
		{"v0.1.0", "0.2.0", false, false},
		{"v0.1.0", "", false, false},
		{"v99999999999999999999.0.0", "v0.1.0", false, false}, // overflows int
		{"xv0.1.0", "v0.1.1", false, false},
		{"v0.1.0", "xv0.1.1", false, false},
		{"v0.1.0", "../v0.1.1", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.current+" vs "+tt.latest, func(t *testing.T) {
			t.Parallel()
			newer, ok := IsNewer(tt.current, tt.latest)
			if newer != tt.newer || ok != tt.ok {
				t.Errorf("IsNewer(%q, %q) = %v, %v; want %v, %v", tt.current, tt.latest, newer, ok, tt.newer, tt.ok)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	// The README and DESIGN state these; the other tests scale with them.
	if maxResponse != 1<<20 || maxNotes != 2000 {
		t.Fatalf("maxResponse = %d, maxNotes = %d", maxResponse, maxNotes)
	}
}

func TestShorten(t *testing.T) {
	t.Parallel()
	exact := strings.Repeat("a", maxNotes)
	if got := shorten(exact, maxNotes); got != exact {
		t.Fatal("notes of exactly the limit should be kept whole")
	}
	// Space at the cut is trimmed before the mark.
	if got := shorten(strings.Repeat("a", maxNotes-1)+"  b", maxNotes); got != strings.Repeat("a", maxNotes-1)+"…" {
		t.Fatalf("shorten = …%q", got[len(got)-8:])
	}
}

func TestPrintable_DropsControlAndFormatCharacters(t *testing.T) {
	t.Parallel()
	in := "Fixes\n\t- one\u202e evil\u200b\x1b[31m\r"
	if got, want := printable(in), "Fixes\n\t- one evil[31m"; got != want {
		t.Fatalf("printable = %q, want %q", got, want)
	}
}

// serve returns a Checker whose requests reach handler. It keeps
// NewChecker's client settings (timeout, redirect policy).
func serve(t *testing.T, handler http.HandlerFunc) *Checker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewChecker()
	c.client.Transport = srv.Client().Transport
	c.url = srv.URL
	return c
}

func TestLatest_ReturnsTheRelease(t *testing.T) {
	t.Parallel()
	var got *http.Request
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.1","html_url":"https://evil.example/","body":"  ## What's new\n- TOTP codes\u202e  "}`))
	})
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.1.1" || rel.Notes != "## What's new\n- TOTP codes" {
		t.Fatalf("release = %+v", rel)
	}
	// The page comes from the tag, never from the answer's html_url.
	if rel.PageURL() != "https://github.com/heyjdp/vlt/releases/tag/v0.1.1" {
		t.Fatalf("page = %q", rel.PageURL())
	}
	if got.Method != http.MethodGet || got.Header.Get("User-Agent") != "vlt-update-check" ||
		got.Header.Get("Accept") != "application/vnd.github+json" || got.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Fatalf("request = %s %v", got.Method, got.Header)
	}
}

func TestLatest_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		status    int
		remaining string // X-RateLimit-Remaining
		body      string
		wantErr   error
		wantMsg   string
	}{
		{"no release yet", http.StatusNotFound, "", `{"message":"Not Found"}`, ErrNoRelease, ""},
		{"rate limited", http.StatusForbidden, "0", `{"message":"API rate limit exceeded"}`, ErrRateLimited, ""},
		{"forbidden for another reason", http.StatusForbidden, "", `blocked`, nil, "403"},
		{"too many requests", http.StatusTooManyRequests, "", ``, ErrRateLimited, ""},
		{"server error", http.StatusBadGateway, "", ``, nil, "502"},
		{"not JSON", http.StatusOK, "", `<html>`, nil, "parse"},
		{"odd tag", http.StatusOK, "", `{"tag_name":"v0.2.0-rc1"}`, nil, "unexpected release tag"},
		{"tag with a path", http.StatusOK, "", `{"tag_name":"v0.1.1/../../evil"}`, nil, "unexpected release tag"},
		{"tag after a path", http.StatusOK, "", `{"tag_name":"../v0.1.1"}`, nil, "unexpected release tag"},
		{"no tag", http.StatusOK, "", `{}`, nil, "unexpected release tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.remaining != "" {
					w.Header().Set("X-RateLimit-Remaining", tt.remaining)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := c.Latest(context.Background())
			if err == nil {
				t.Fatal("no error")
			}
			if tt.wantErr == nil && (errors.Is(err, ErrRateLimited) || errors.Is(err, ErrNoRelease)) {
				t.Fatalf("err = %v, want a plain error", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

func TestLatest_ShortensLongNotesAndReadsAtMostAMegabyte(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", maxNotes+10)
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.1","body":"` + long + `"}`))
	})
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(rel.Notes); n != maxNotes+1 || !strings.HasSuffix(rel.Notes, "…") {
		t.Fatalf("notes are %d runes, ending %q", n, rel.Notes[len(rel.Notes)-4:])
	}

	// Just under the limit is read.
	big := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.1","body":"` + strings.Repeat("a", 900<<10) + `"}`))
	})
	if _, err := big.Latest(context.Background()); err != nil {
		t.Fatalf("a 900 KiB answer: %v", err)
	}

	huge := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.1","body":"` + strings.Repeat("a", maxResponse) + `"}`))
	})
	if _, err := huge.Latest(context.Background()); err == nil {
		t.Fatal("an answer over the limit was read whole")
	}
}

func TestLatest_StopsWhenCancelled(t *testing.T) {
	t.Parallel()
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.1"}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Latest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewChecker_AsksGitHubWithATimeout(t *testing.T) {
	t.Parallel()
	c := NewChecker()
	if c.url != "https://api.github.com/repos/heyjdp/vlt/releases/latest" || c.client.Timeout != requestTimeout {
		t.Fatalf("checker = %q, timeout %v", c.url, c.client.Timeout)
	}
}

func TestLatest_FollowsRedirectsOnlyToGitHubOverHTTPS(t *testing.T) {
	t.Parallel()
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example/latest", http.StatusFound)
	})
	_, err := c.Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing a redirect to http://elsewhere.example/latest") {
		t.Fatalf("err = %v", err)
	}

	for _, tt := range []struct {
		url string
		ok  bool
	}{
		{"https://api.github.com/repositories/1/releases/latest", true},
		{"http://api.github.com/x", false},
		{"https://api.github.com.evil.example/x", false},
		{"https://github.com/x", false},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.url, http.NoBody)
		if err := githubOnly(req, nil); (err == nil) != tt.ok {
			t.Errorf("githubOnly(%s) = %v", tt.url, err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://api.github.com/x", http.NoBody)
	if githubOnly(req, make([]*http.Request, 5)) == nil {
		t.Error("a sixth redirect should be refused")
	}
}
