package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/heyjdp/vlt/internal/store"
)

func TestParseLocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want store.Location
	}{
		// Local paths.
		{"/Users/me/vault", store.Location{Path: "/Users/me/vault"}},
		{"  /srv/vault  ", store.Location{Path: "/srv/vault"}},
		{`C:\Users\me\vault`, store.Location{Path: `C:\Users\me\vault`}},
		{"C:/vault", store.Location{Path: "C:/vault"}},
		{"./vault:old", store.Location{Path: "./vault:old"}},
		{"/a/b:c", store.Location{Path: "/a/b:c"}},
		{`\\server\share\vault`, store.Location{Path: `\\server\share\vault`}},
		{"vault", store.Location{Path: "vault"}},
		// scp-style shorthand.
		{"192.0.2.10:/home/me/vault", store.Location{Host: "192.0.2.10", Path: "/home/me/vault"}},
		{"me@192.0.2.10:/home/me/vault", store.Location{Host: "192.0.2.10", User: "me", Path: "/home/me/vault"}},
		{"nas:vault", store.Location{Host: "nas", Path: "vault"}},
		{"nas:~/vaults/main", store.Location{Host: "nas", Path: "vaults/main"}},
		{"[fd00::1]:/srv/vault", store.Location{Host: "fd00::1", Path: "/srv/vault"}},
		{"me@[fd00::1]:/srv/vault", store.Location{Host: "fd00::1", User: "me", Path: "/srv/vault"}},
		// ssh:// URLs.
		{"ssh://me@192.0.2.10/home/me/vault", store.Location{Host: "192.0.2.10", User: "me", Path: "/home/me/vault"}},
		{"ssh://nas:2222/srv/vault", store.Location{Host: "nas", Port: 2222, Path: "/srv/vault"}},
		{"ssh://nas/~/vault", store.Location{Host: "nas", Path: "vault"}},
		{"SSH://nas/~/vault", store.Location{Host: "nas", Path: "vault"}},
		{"ssh://nas/~//vault", store.Location{Host: "nas", Path: "vault"}},
		{"nas:~//vault", store.Location{Host: "nas", Path: "vault"}},
		{"ssh://nas/%7E/vault", store.Location{Host: "nas", Path: "/~/vault"}},
		{"ssh://nas:2222/srv/my%20vault%231", store.Location{Host: "nas", Port: 2222, Path: "/srv/my vault#1"}},
		{"ssh://x/srv/vault", store.Location{Host: "x", Path: "/srv/vault"}},
		{"ssh://[fd00::1]:22/srv/vault", store.Location{Host: "fd00::1", Port: 22, Path: "/srv/vault"}},
		{"ssh://[fe80::1%25en0]/srv/vault", store.Location{Host: "fe80::1%en0", Path: "/srv/vault"}},
		{"my_user.name@nas-01.local:vault", store.Location{Host: "nas-01.local", User: "my_user.name", Path: "vault"}},
	}
	for _, tt := range tests {
		got, err := store.ParseLocation(tt.in)
		if err != nil {
			t.Errorf("ParseLocation(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseLocation(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseLocation_Rejects(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"",
		"   ",
		"nas:",                        // no folder
		"nas:~",                       // the login directory itself
		"nas:/",                       // the server's root
		"ssh://nas",                   // no folder
		"ssh://nas/",                  // root
		"ssh://me:hunter2@nas/vault",  // password
		"ssh://nas/vault?x=1",         // query
		"ssh://nas:99999/vault",       // port
		"-oProxyCommand=evil:/vault",  // host that ssh would read as an option
		"ssh://-oProxyCommand=evil/x", // the same in a URL
		"-l@nas:/vault",               // user that looks like an option
		"ssh://ev%20il@nas/vault",     // whitespace in the user
		"h;id:/vault",                 // shell metacharacter in the host
		"ev$il@nas:/vault",            // shell metacharacter in the user
		"ssh://a%3Ab@nas/vault",       // a colon in the user would not round-trip
		"ssh://n%25as/vault",          // % outside an IPv6 address
		"[:]:srv",                     // a host that is only a colon
		"nas]:/srv/vault",             // a stray bracket in the host
		"ssh://nas/x%00y",             // a NUL, where a C server would cut the name
		"nas:/srv/a\tb",               // another control character
		"ssh://::/vault",              // the same in a URL
		"ssh://[::::]/vault",          // colons that aren't an IPv6 address
		"ssh://[fd00::1%25]/vault",    // an empty IPv6 zone
		"[fd00::1:/srv/vault",         // unclosed IPv6 bracket reads as a local path, so check it below
	} {
		loc, err := store.ParseLocation(in)
		if in == "[fd00::1:/srv/vault" {
			if err != nil || loc.IsRemote() {
				t.Errorf("ParseLocation(%q) = %+v, %v; want a local path", in, loc, err)
			}
			continue
		}
		if !errors.Is(err, store.ErrBadLocation) {
			t.Errorf("ParseLocation(%q) = %+v, %v; want ErrBadLocation", in, loc, err)
		}
	}
}

func TestLocation_StringRoundTrips(t *testing.T) {
	t.Parallel()
	for _, loc := range []store.Location{
		{Path: "/Users/me/vault"},
		{Host: "192.0.2.10", Path: "/home/me/vault"},
		{Host: "192.0.2.10", User: "me", Path: "/home/me/vault"},
		{Host: "nas", Path: "vaults/main"},
		{Host: "nas", Port: 2222, Path: "/srv/vault"},
		{Host: "nas", User: "me", Port: 2222, Path: "vault"},
		{Host: "x", Path: "/srv/vault"},
		{Host: "fd00::1", Path: "/srv/vault"},
		{Host: "fd00::1", Port: 22, User: "me", Path: "vault"},
		{Host: "fe80::1%en0", Path: "/srv/vault"},
		// Characters a URL would read as its own: in an ssh:// location they
		// must be escaped, or the location saved in Settings can't be read.
		{Host: "nas", Port: 2222, Path: "/srv/a#b"},
		{Host: "nas", Port: 2222, Path: "/srv/a?b"},
		{Host: "nas", Port: 2222, Path: "/srv/100%"},
		{Host: "nas", Port: 2222, Path: "/srv/my vault"},
		{Host: "x", Path: "vaults/#1"},
		{Host: "nas", Path: "/srv/a#b?c%d"},
		{Host: "00", Path: "@["},
		{Host: "nas", Path: "/srv/vault "},
		{Host: "nas", Path: " vault"},
		{Host: "ssh", Path: "//srv/vault"},
		{Host: "SSH", Path: "//srv/vault"},
		{Host: "x", Path: "/~"},
		{Host: "x", Path: "/~/vault #1"},
		{Host: "nas", Port: 2222, Path: "/~/vault"},
		{Host: "nas", User: "me", Path: "/srv/x@[y]"},
	} {
		s := loc.String()
		back, err := store.ParseLocation(s)
		if err != nil || back != loc {
			t.Errorf("%+v -> %q -> %+v, %v", loc, s, back, err)
		}
	}
	if got := (store.Location{Host: "192.0.2.10", User: "me", Path: "/home/me/vault"}).String(); got != "me@192.0.2.10:/home/me/vault" {
		t.Errorf("String() = %q, want the shorthand", got)
	}
}

// FuzzParseLocation checks that every remote location ParseLocation accepts
// has a host and user ssh can't misread, and reads back from String as
// itself: Settings saves String's form.
func FuzzParseLocation(f *testing.F) {
	for _, s := range []string{
		"/a", "nas:vault", "me@[fd00::1]:/srv/v", "ssh://me@h:22/~/v",
		"ssh://[fe80::1%25en0]/srv", "a@b@c:x", "-oProxyCommand=x:y",
		"ssh://nas:2222/srv/a%23b", "ssh://nas:2222/srv/100%25", "[:]:srv",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		loc, err := store.ParseLocation(s)
		if err != nil || !loc.IsRemote() {
			return
		}
		for _, name := range []string{loc.Host, loc.User} {
			if strings.HasPrefix(name, "-") || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-:%") != "" {
				t.Fatalf("ParseLocation(%q) accepted host or user %q", s, name)
			}
		}
		back, err := store.ParseLocation(loc.String())
		if err != nil || back != loc {
			t.Fatalf("%q -> %+v -> %q -> %+v, %v", s, loc, loc.String(), back, err)
		}
	})
}
