#!/bin/sh
# Verify that every component version matches the VERSION file.
# Usage: scripts/check-version.sh [vX.Y.Z]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

VERSION=$(tr -d '[:space:]' < VERSION)
TAG=${1:-}

fail=0
err() {
	printf 'version mismatch: %s\n' "$*" >&2
	fail=1
}

case "$VERSION" in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) err "VERSION is not semver: $VERSION" ;;
esac

if [ -n "$TAG" ] && [ "v$VERSION" != "$TAG" ]; then
	err "tag $TAG does not match VERSION v$VERSION"
fi

check_line() {
	file=$1
	label=$2
	want=$3
	got=$4
	if [ "$got" != "$want" ]; then
		err "$label in $file = '$got', want '$want'"
	fi
}

buildinfo=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/buildinfo/buildinfo.go | head -n 1)
check_line internal/buildinfo/buildinfo.go "Version" "$VERSION" "$buildinfo"

pkg=$(sed -n 's/^[[:space:]]*"version": "\(.*\)",$/\1/p' deploy/docker/web-build/package.json | head -n 1)
check_line deploy/docker/web-build/package.json "version" "$VERSION" "$pkg"

pkg_lock=$(sed -n 's/^[[:space:]]*"version": "\(.*\)",$/\1/p' deploy/docker/web-build/package-lock.json | head -n 1)
check_line deploy/docker/web-build/package-lock.json "version" "$VERSION" "$pkg_lock"

android=$(sed -n 's/.*versionName "\([^"]*\)".*/\1/p' android/app-native/build.gradle | head -n 1)
check_line android/app-native/build.gradle "versionName" "$VERSION" "$android"

if [ "$fail" -ne 0 ]; then
	exit 1
fi

printf 'version ok: %s\n' "$VERSION"
