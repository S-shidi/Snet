#!/usr/bin/env bash
# Build versioned release binaries and checksums into dist/.
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="$(tr -d '[:space:]' < VERSION)"
COMMIT="$(git describe --tags --always --dirty)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
DIST="dist"
LDFLAGS="-s -w -X snet/internal/buildinfo.Version=${VERSION} -X snet/internal/buildinfo.Commit=${COMMIT} -X snet/internal/buildinfo.BuildDate=${BUILD_DATE}"

rm -rf "${DIST}"
mkdir -p "${DIST}"

build_one() {
  local goos="$1" goarch="$2" name="$3" pkg="$4"
  local out="${DIST}/${name}-${goos}-${goarch}"
  echo "building ${out}"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags="${LDFLAGS}" -o "${out}" "${pkg}"
}

for os in linux darwin; do
  for arch in amd64 arm64; do
    build_one "${os}" "${arch}" server ./cmd/server
    build_one "${os}" "${arch}" snetserv ./cmd/snetserv
    build_one "${os}" "${arch}" admin-server ./cmd/admin-server
    build_one "${os}" "${arch}" relay ./cmd/relay
    build_one "${os}" "${arch}" snetd ./cmd/client/snetd
    build_one "${os}" "${arch}" snetctl ./cmd/client/snetctl
  done
done

(
  cd "${DIST}"
  find . -maxdepth 1 -type f ! -name checksums.txt -print0 \
    | sort -z \
    | xargs -0 sha256sum > checksums.txt
)

echo "release ${VERSION} (${COMMIT}, ${BUILD_DATE})"
ls -lh "${DIST}"
