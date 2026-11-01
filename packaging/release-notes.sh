#!/usr/bin/env bash
# Prints the CHANGELOG.md section of a release, for its GitHub release notes.
#
#   packaging/release-notes.sh <tag> [changelog]
#
# <tag> is vX.Y.Z. The section is the one headed "## [X.Y.Z] - YYYY-MM-DD",
# up to the next "## " heading. It fails if the tag is not a release tag, if
# there is no such section or it has no date, or if the section is empty, so
# a release can't be published before its changelog entry is written.
# Windows line endings are accepted.
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
	echo "usage: $0 <tag> [changelog]" >&2
	exit 2
fi
tag=$1
changelog=${2:-CHANGELOG.md}

if [[ ! $tag =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
	echo "$0: $tag is not a release tag (vX.Y.Z); pre-releases are not supported" >&2
	exit 1
fi
version=${BASH_REMATCH[1]}

if [[ ! -r $changelog ]]; then
	echo "$0: cannot read $changelog" >&2
	exit 1
fi
text=$(tr -d '\r' <"$changelog")

# The dots are escaped so the version matches literally. The date is spelled
# out rather than written [0-9]{4}: older mawk ignores repeat counts.
d='[0-9]'
heading="^## \\[${version//./\\.}\\] - $d$d$d$d-$d$d-$d$d\$"
if ! printf '%s\n' "$text" | grep -Eq "$heading"; then
	echo "$0: $changelog has no \"## [$version] - YYYY-MM-DD\" section; move the Unreleased entries under it first" >&2
	exit 1
fi

# The section runs from its heading to the next "## " heading (a second
# heading for the same version ends it too). The pattern goes through the
# environment: awk -v would eat its backslashes.
notes=$(printf '%s\n' "$text" | HEADING=$heading awk '
	found && /^## / { exit }
	$0 ~ ENVIRON["HEADING"] { found = 1; next }
	found { print }
')

# Drop lines before the first that isn't white space ($(...) has already
# dropped trailing newlines), and fail if nothing is left.
notes=$(printf '%s\n' "$notes" | sed -e '/[^[:space:]]/,$!d')
if [[ -z $notes ]]; then
	echo "$0: the $version section of $changelog is empty" >&2
	exit 1
fi
printf '%s\n' "$notes"
