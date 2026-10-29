package store_test

import (
	"errors"
	"testing"

	"github.com/yourorg/vlt/internal/store"
)

func TestParseLocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want store.Location
	}{
		// Local paths.
		{"/Users/jas/vault", store.Location{Path: "/Users/jas/vault"}},
		{"  /srv/vault  ", store.Location{Path: "/srv/vault"}},
		{`C:\Users\jas\vault`, store.Location{Path: `C:\Users\jas\vault`}},
		{"C:/vault", store.Location{Path: "C:/vault"}},
		{"./vault:old", store.Location{Path: "./vault:old"}},
		{"/a/b:c", store.Location{Path: "/a/b:c"}},
		{`\\server\share\vault`, store.Location{Path: `\\server\share\vault`}},
		{"vault", store.Location{Path: "vault"}},
		// scp-style shorthand.
		{"10.20.20.150:/home/jas/vault", store.Location{Host: "10.20.20.150", Path: "/home/jas/vault"}},
		{"jas@10.20.20.150:/home/jas/vault", store.Location{Host: "10.20.20.150", User: "jas", Path: "/home/jas/vault"}},
		{"nas:vault", store.Location{Host: "nas", Path: "vault"}},
		{"nas:~/vaults/main", store.Location{Host: "nas", Path: "vaults/main"}},
		{"[fd00::1]:/srv/vault", store.Location{Host: "fd00::1", Path: "/srv/vault"}},
		{"me@[fd00::1]:/srv/vault", store.Location{Host: "fd00::1", User: "me", Path: "/srv/vault"}},
		// ssh:// URLs.
		{"ssh://jas@10.20.20.150/home/jas/vault", store.Location{Host: "10.20.20.150", User: "jas", Path: "/home/jas/vault"}},
		{"ssh://nas:2222/srv/vault", store.Location{Host: "nas", Port: 2222, Path: "/srv/vault"}},
		{"ssh://nas/~/vault", store.Location{Host: "nas", Path: "vault"}},
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
		"ssh://jas:hunter2@nas/vault", // password
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
		{Path: "/Users/jas/vault"},
		{Host: "10.20.20.150", Path: "/home/jas/vault"},
		{Host: "10.20.20.150", User: "jas", Path: "/home/jas/vault"},
		{Host: "nas", Path: "vaults/main"},
		{Host: "nas", Port: 2222, Path: "/srv/vault"},
		{Host: "nas", User: "jas", Port: 2222, Path: "vault"},
		{Host: "x", Path: "/srv/vault"},
		{Host: "fd00::1", Path: "/srv/vault"},
		{Host: "fd00::1", Port: 22, User: "me", Path: "vault"},
	} {
		s := loc.String()
		back, err := store.ParseLocation(s)
		if err != nil || back != loc {
			t.Errorf("%+v -> %q -> %+v, %v", loc, s, back, err)
		}
	}
	if got := (store.Location{Host: "10.20.20.150", User: "jas", Path: "/home/jas/vault"}).String(); got != "jas@10.20.20.150:/home/jas/vault" {
		t.Errorf("String() = %q, want the shorthand", got)
	}
}
