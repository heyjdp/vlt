#!/usr/bin/env bash
# Tests packaging/release-notes.sh: run it from the repository root.
set -euo pipefail

script=packaging/release-notes.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failed=0

# expect <want status> <want output> <tag> <changelog text>
expect() {
	printf '%s' "$4" >"$work/CHANGELOG.md"
	local out status=0
	out=$("$script" "$3" "$work/CHANGELOG.md" 2>/dev/null) || status=$?
	if [[ $status != "$1" || $out != "$2" ]]; then
		printf 'FAIL %s: status %s, output %q; want %s, %q\n' "$3" "$status" "$out" "$1" "$2" >&2
		failed=1
	fi
}

changelog='# Changelog

## [Unreleased]

### Added
- Not yet released.

## [0.1.1] - 2026-11-02

### Fixed
- One.

## [0.1.0] - 2026-10-29

### Added
- Two.
'
expect 0 $'### Fixed\n- One.' v0.1.1 "$changelog"
expect 0 $'### Added\n- Two.' v0.1.0 "$changelog"                     # the last section
expect 1 '' v0.1.2 "$changelog"                                       # no section
expect 1 '' 0.1.1 "$changelog"                                        # not a tag
expect 1 '' v0.1.1-rc1 "$changelog"                                   # a pre-release
expect 1 '' v0.1.1 $'## [0.1.1]\n- Undated.\n'                        # no date
expect 1 '' v0.1.1 $'## [0.1.1] - 2026-11-02\n\n\n## [0.1.0] - 2026-10-29\n- Two.\n' # empty
expect 1 '' v0.1.1 $'## [0x1x1] - 2026-11-02\n- Dots are literal.\n'
expect 1 '' v0.1.1 $'## [10.1.1] - 2026-11-02\n- A longer version.\n'
expect 1 '' refs/tags/v0.1.1 "$changelog"                             # github.ref, not ref_name
expect 1 '' v0.1.1 $'## [0.1.1] - 2026-11-022\n- Bad date.\n'
expect 1 '' v0.1.1 $'## [0.1.1] - 2026-11\n- Bad date.\n'
expect 1 '' v0.1.1 $'### [0.1.1] - 2026-11-02\n- Not a release heading.\n'
expect 1 '' v0.1.1 $'## [0.1.1] - 2026-11-02\n  \n\t\n## [0.1.0] - 2026-10-29\n- Two.\n' # white space only
expect 0 '- One.' v0.1.1 $'## [0.1.1] - 2026-11-02\n- One.\n\n## Notes\n- Not notes.\n'      # any ## heading ends it
expect 0 '- One.' v0.1.1 $'## [0.1.1] - 2026-11-02\n- One.\n## [0.1.1] - 2026-11-03\n- Again.\n'
expect 0 $'### Fixed\n- One.' v0.1.1 "${changelog//$'\n'/$'\r\n'}"      # Windows line endings

# Usage errors exit 2; output ends with a newline; the default file is
# CHANGELOG.md in the current directory.
status=0
"$script" >/dev/null 2>&1 || status=$?
[[ $status == 2 ]] || { echo "FAIL: no arguments exit $status, want 2" >&2; failed=1; }
status=0
"$script" a b c >/dev/null 2>&1 || status=$?
[[ $status == 2 ]] || { echo "FAIL: three arguments exit $status, want 2" >&2; failed=1; }
printf '%s' "$changelog" >"$work/CHANGELOG.md"
last=$(cd "$work" && "$OLDPWD/$script" v0.1.1 | tail -c 1 | od -An -tx1 | tr -d ' ')
[[ $last == 0a ]] || { echo "FAIL: default CHANGELOG.md or final newline ($last)" >&2; failed=1; }
status=0
"$script" v0.1.1 "$work/missing.md" >/dev/null 2>&1 || status=$?
[[ $status == 1 ]] || { echo "FAIL: a missing changelog exit $status, want 1" >&2; failed=1; }

# The real changelog keeps the notes of every published release: v0.1.0
# (named, in case the clone has no tags) and every release tag it knows.
for tag in v0.1.0 $(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true); do
	if ! "$script" "$tag" >/dev/null; then
		echo "FAIL: CHANGELOG.md has no notes for $tag" >&2
		failed=1
	fi
done

if [[ $failed != 0 ]]; then
	exit 1
fi
echo "release-notes.sh: ok"
