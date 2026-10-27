#!/usr/bin/env bash
# Builds vlt.app from a vlt binary, then zips it.
#
#   packaging/macos/build-app.sh <vlt binary> <app ID> <version> <build> <min macOS> <zip>
#
# <version> must be x.y.z (CFBundleShortVersionString); <build> is an
# increasing integer. The bundle is ad-hoc signed, not notarised. Uses only
# tools that ship with macOS: sips, iconutil, plutil, codesign and ditto.
set -euo pipefail

if [[ $# -ne 6 ]]; then
	echo "usage: $0 <vlt binary> <app ID> <version> <build> <min macOS> <zip>" >&2
	exit 2
fi
binary=$1
app_id=$2
version=$3
build=$4
macos_min=$5
zip=$6
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "version must be x.y.z, got $version" >&2
	exit 2
fi
if [[ ! $app_id =~ ^[A-Za-z0-9.-]+$ ]]; then
	echo "app ID must be reverse-DNS (letters, digits, . and -), got $app_id" >&2
	exit 2
fi

app=vlt.app
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$binary" "$app/Contents/MacOS/vlt"

# Icon: every size macOS asks for, from the 1024px source.
iconset=$work/vlt.iconset
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$root/Icon.png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
	sips -z $((size * 2)) $((size * 2)) "$root/Icon.png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil --convert icns "$iconset" --output "$app/Contents/Resources/vlt.icns"

sed -e "s/@APP_ID@/$app_id/" -e "s/@VERSION@/$version/" -e "s/@BUILD@/$build/" -e "s/@MACOS_MIN@/$macos_min/" \
	"$here/Info.plist" >"$app/Contents/Info.plist"
plutil -lint -s "$app/Contents/Info.plist"

codesign --force --sign - --timestamp=none "$app"
codesign --verify --deep --strict "$app"

mkdir -p "$(dirname "$zip")"
ditto -c -k --keepParent "$app" "$zip"
echo "built $app and $zip"
