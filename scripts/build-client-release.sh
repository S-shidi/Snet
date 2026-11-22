#!/usr/bin/env bash
# Build versioned client binaries and checksums into dist/ and dist-deploy/.
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="$(tr -d '[:space:]' < VERSION)"
COMMIT="$(git describe --tags --always --dirty 2>/dev/null || printf 'unknown')"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X github.com/S-shidi/snet/shared/buildinfo.Version=${VERSION} -X github.com/S-shidi/snet/shared/buildinfo.Commit=${COMMIT} -X github.com/S-shidi/snet/shared/buildinfo.BuildDate=${BUILD_DATE}"

rm -rf dist dist-deploy
mkdir -p dist dist-deploy

build_one() {
  local goos="$1" goarch="$2" name="$3" pkg="$4" out="$5"
  echo "building ${out}"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" -o "${out}" "${pkg}"
}

for os in linux darwin; do
  for arch in amd64 arm64; do
    build_one "${os}" "${arch}" snetd ./cmd/client/snetd "dist/snetd-${os}-${arch}"
    build_one "${os}" "${arch}" snetctl ./cmd/client/snetctl "dist/snetctl-${os}-${arch}"
  done
done

for arch in amd64 arm64; do
  mkdir -p "dist-deploy/linux-${arch}"
  build_one linux "${arch}" snetd ./cmd/client/snetd "dist-deploy/linux-${arch}/snetd"
  build_one linux "${arch}" snetctl ./cmd/client/snetctl "dist-deploy/linux-${arch}/snetctl"
  cp "dist-deploy/linux-${arch}/snetd" "dist-deploy/snetd-linux-${arch}"
  cp "dist-deploy/linux-${arch}/snetctl" "dist-deploy/snetctl-linux-${arch}"
done

(
  cd dist
  find . -maxdepth 1 -type f ! -name checksums.txt -print0 \
    | sort -z \
    | xargs -0 sha256sum > checksums.txt
)
(
  cd dist-deploy
  find . -maxdepth 1 -type f ! -name checksums.txt -print0 \
    | sort -z \
    | xargs -0 sha256sum > checksums.txt
)

echo "release ${VERSION} (${COMMIT}, ${BUILD_DATE})"
ls -lh dist dist-deploy/linux-amd64
