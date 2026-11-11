package store

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Location is where a vault lives: a local directory, or a directory on an
// SSH server reached with the system's ssh client (see sshfs.go).
type Location struct {
	// Host is the SSH server (a name from ~/.ssh/config, a hostname or an
	// IP address). It is empty for a local vault.
	Host string
	// User and Port override ~/.ssh/config when set.
	User string
	Port int
	// Path is the vault directory. On a server, a relative path is relative
	// to the login directory.
	Path string
}

// ErrBadLocation is returned by ParseLocation for a location it cannot use.
var ErrBadLocation = errors.New("invalid vault location")

// ParseLocation parses a vault location. It accepts:
//
//   - a local path: /Users/me/vault, C:\vault, ./vault
//   - an SSH URL: ssh://[user@]host[:port]/absolute/path, or
//     ssh://[user@]host[:port]/~/path for a path under the login directory
//   - scp-style shorthand: [user@]host:path, where path is absolute or
//     relative to the login directory (~/ may be written)
//
// Shorthand needs a host of two or more characters, so a Windows drive
// letter (C:\vault) is always a local path; use ssh:// for one-letter host
// names. A local path whose first colon precedes any slash (vault:old) reads
// as shorthand; write it as ./vault:old.
func ParseLocation(s string) (Location, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Location{}, fmt.Errorf("%w: empty", ErrBadLocation)
	}
	if hasSSHScheme(s) {
		return parseSSHURL(s)
	}
	if host, path, ok := splitShorthand(s); ok {
		loc := Location{Host: host, Path: homeRelative(path)}
		if i := strings.LastIndex(host, "@"); i >= 0 {
			loc.User, loc.Host = host[:i], host[i+1:]
		}
		if strings.HasPrefix(loc.Host, "[") && strings.HasSuffix(loc.Host, "]") {
			loc.Host = loc.Host[1 : len(loc.Host)-1]
		}
		return loc, loc.validate(s)
	}
	return Location{Path: s}, nil
}

// hasSSHScheme reports whether s starts with ssh://, in any case.
func hasSSHScheme(s string) bool {
	const scheme = "ssh://"
	return len(s) >= len(scheme) && strings.EqualFold(s[:len(scheme)], scheme)
}

// splitShorthand splits [user@]host:path. It reports false for anything that
// should be read as a local path: no colon, a slash or backslash before the
// first colon, or a one-character host (a Windows drive letter).
func splitShorthand(s string) (host, path string, ok bool) {
	end := 0
	colon := strings.Index(s, ":")
	if at := strings.Index(s, "@["); strings.HasPrefix(s, "[") || (at >= 0 && (colon < 0 || at < colon)) {
		// An IPv6 literal, [::1]:path; its colons are inside the brackets.
		// A "@[" after the first colon is in the path.
		end = strings.Index(s, "]")
		if end < 0 {
			return "", "", false
		}
	}
	i := strings.Index(s[end:], ":")
	if i < 0 {
		return "", "", false
	}
	i += end
	host, path = s[:i], s[i+1:]
	if len(host) < 2 || strings.ContainsAny(host, `/\`) {
		return "", "", false
	}
	return host, path, true
}

// parseSSHURL parses ssh://[user@]host[:port]/path.
func parseSSHURL(s string) (Location, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Location{}, fmt.Errorf("%w: %w", ErrBadLocation, err)
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return Location{}, fmt.Errorf("%w: passwords are not accepted in the location; use an SSH key", ErrBadLocation)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Location{}, fmt.Errorf("%w: %q has a query or fragment", ErrBadLocation, s)
	}
	loc := Location{Host: u.Hostname(), User: u.User.Username(), Path: u.Path}
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return Location{}, fmt.Errorf("%w: port %q", ErrBadLocation, p)
		}
		loc.Port = port
	}
	// Only a literal ~ means the login directory; String writes a folder
	// named ~ at the root as %7E.
	switch escaped := u.EscapedPath(); {
	case escaped == "/~" || escaped == "/~/":
		loc.Path = ""
	case strings.HasPrefix(escaped, "/~/"):
		loc.Path = homeRelative(loc.Path[len("/"):])
	}
	return loc, loc.validate(s)
}

// homeRelative turns ~/path into path: the SFTP server resolves relative
// paths against the login directory, and it does not expand ~. Further
// slashes are dropped too, so ~//x stays under the login directory rather
// than becoming /x.
func homeRelative(path string) string {
	if path == "~" {
		return ""
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return strings.TrimLeft(rest, "/")
	}
	return path
}

// validate checks a remote location's parts. Host and user are passed to
// ssh as arguments, and ssh can substitute them into commands from the
// user's ssh config (%h, %u in ProxyCommand or Match exec), so only plain
// name characters are allowed: letters, digits, '.', '_' and '-' (no leading
// '-'), plus ':' and '%' in an IPv6 address.
func (l Location) validate(original string) error {
	if l.Host == "" {
		return fmt.Errorf("%w: %q has no host", ErrBadLocation, original)
	}
	switch {
	case l.Path == "":
		return fmt.Errorf("%w: %q names no folder on the server", ErrBadLocation, original)
	case strings.Trim(l.Path, "/") == "":
		return fmt.Errorf("%w: a vault cannot be the server's root folder", ErrBadLocation)
	case strings.ContainsFunc(l.Path, unicode.IsControl):
		// A C server would cut the name at a NUL, and use another folder.
		return fmt.Errorf("%w: the folder name holds a control character", ErrBadLocation)
	}
	ipv6 := strings.Contains(l.Host, ":")
	if ipv6 && !isIPv6(l.Host) {
		return fmt.Errorf("%w: host %q is not an IPv6 address", ErrBadLocation, l.Host)
	}
	if !plainName(l.Host, ipv6) {
		return fmt.Errorf("%w: host %q may only hold letters, digits, '.', '_' and '-'", ErrBadLocation, l.Host)
	}
	if l.User != "" && !plainName(l.User, false) {
		return fmt.Errorf("%w: user %q may only hold letters, digits, '.', '_' and '-'", ErrBadLocation, l.User)
	}
	return nil
}

// isIPv6 reports whether host is an IPv6 address, with an optional zone.
func isIPv6(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6()
}

// plainName reports whether s is a non-empty name of letters, digits, '.',
// '_' and '-' that does not start with '-'; ipv6 also allows ':' and '%'
// (a zone, as in fe80::1%en0).
func plainName(s string, ipv6 bool) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		case ipv6 && (r == ':' || r == '%'):
		default:
			return false
		}
	}
	return true
}

// IsRemote reports whether the vault is on an SSH server.
func (l Location) IsRemote() bool { return l.Host != "" }

// String returns the location in the form ParseLocation reads back: the
// path for a local vault; [user@]host:path for a remote one (an IPv6 host
// in brackets), or an ssh:// URL when a port is set, the host has one
// character (shorthand would read as a drive letter), or the path starts or
// ends with white space (which ParseLocation trims; the URL escapes it), or
// the shorthand would itself start with ssh://, in any case (host "ssh",
// path "//x").
func (l Location) String() string {
	if !l.IsRemote() {
		return l.Path
	}
	host := l.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	user := ""
	if l.User != "" {
		user = l.User + "@"
	}
	if l.Port == 0 && len(l.Host) > 1 && strings.TrimSpace(l.Path) == l.Path {
		path := l.Path
		if !strings.HasPrefix(path, "/") {
			path = "~/" + path
		}
		if short := user + host + ":" + path; !hasSSHScheme(short) {
			return short
		}
	}
	if l.Port != 0 {
		host += ":" + strconv.Itoa(l.Port)
	}
	path, raw := l.Path, ""
	switch {
	case !strings.HasPrefix(path, "/"):
		path = "/~/" + path
	case path == "/~" || strings.HasPrefix(path, "/~/"):
		// A folder named ~ at the root, not the login directory.
		raw = "/%7E" + escapePath(path[len("/~"):])
	}
	// url.URL escapes the path, so '#', '?' and '%' in it read back as
	// themselves rather than as a fragment, a query or an escape.
	u := url.URL{Scheme: "ssh", Host: host, Path: path, RawPath: raw}
	if l.User != "" {
		u.User = url.User(l.User)
	}
	return u.String()
}

// escapePath escapes a URL path as url.URL.String does.
func escapePath(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}

// Server returns [user@]host for messages, or "" for a local vault.
func (l Location) Server() string {
	if l.User != "" {
		return l.User + "@" + l.Host
	}
	return l.Host
}
