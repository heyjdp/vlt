#!/usr/bin/env bash
# Builds vlt-<version>-x86_64.AppImage from a vlt binary.
#
#   packaging/linux/build-appimage.sh <vlt binary> <version> <output dir>
#
# appimagetool and the AppImage runtime are downloaded at pinned versions and
# checked against their SHA-256 before use. Run it on the oldest Linux you
# support: the AppImage needs a glibc at least as new as the build machine's.
set -euo pipefail

if [[ $# -ne 3 ]]; then
	echo "usage: $0 <vlt binary> <version> <output dir>" >&2
	exit 2
fi
binary=$1
version=$2
outdir=$3
here=$(cd "$(dirname "$0")" && pwd)

# Pinned tools: appimagetool 1.9.1 and the type2 runtime release 20251108.
tool_url=https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage
tool_sha=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
runtime_url=https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64
runtime_sha=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fetch() { # url sha256 destination
	curl --fail --silent --show-error --location --retry 3 --output "$3" "$1"
	echo "$2  $3" | sha256sum --check --quiet
}
fetch "$tool_url" "$tool_sha" "$work/appimagetool"
fetch "$runtime_url" "$runtime_sha" "$work/runtime"
chmod +x "$work/appimagetool"

# AppDir layout: the binary, the desktop entry and icon at the root (as the
# AppImage spec requires) and in their usual places under usr/share.
appdir=$work/vlt.AppDir
install -Dm755 "$binary" "$appdir/usr/bin/vlt"
install -Dm644 "$here/vlt.desktop" "$appdir/usr/share/applications/vlt.desktop"
install -Dm644 "$here/vlt.png" "$appdir/usr/share/icons/hicolor/256x256/apps/vlt.png"
cp "$here/vlt.desktop" "$here/vlt.png" "$appdir/"
ln -s usr/bin/vlt "$appdir/AppRun"

mkdir -p "$outdir"
out=$outdir/vlt-$version-x86_64.AppImage
# appimagetool is itself an AppImage; extract-and-run avoids needing FUSE.
APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 VERSION=$version \
	"$work/appimagetool" --no-appstream --runtime-file "$work/runtime" "$appdir" "$out"
echo "built $out"
