#!/bin/sh
# Bump all component versions to X.Y.Z; increment Android versionCode when changed.
# Usage: scripts/bump-version.sh 1.2.0
set -eu

if [ $# -ne 1 ]; then
	echo "usage: $0 X.Y.Z" >&2
	exit 2
fi

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

python3 - "$1" <<'PY'
import pathlib
import re
import sys

version = sys.argv[1]
if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", version):
    raise SystemExit(f"invalid semver: {version}")

old_version = pathlib.Path("VERSION").read_text().strip()

def read(path):
    return pathlib.Path(path).read_text()

def write(path, text):
    pathlib.Path(path).write_text(text)

write("VERSION", version + "\n")

p = "internal/buildinfo/buildinfo.go"
s = read(p)
s, n = re.subn(r'^var Version = ".*"$', f'var Version = "{version}"', s, count=1, flags=re.M)
if n != 1:
    raise SystemExit(f"cannot update {p}")
write(p, s)

p = "deploy/docker/web-build/package.json"
s = read(p)
s, n = re.subn(r'("version": ")[^"]+(")', rf'\g<1>{version}\g<2>', s, count=1)
if n != 1:
    raise SystemExit(f"cannot update {p}")
write(p, s)

p = "deploy/docker/web-build/package-lock.json"
s = read(p)
s, n1 = re.subn(r'("version": ")[^"]+(")', rf'\g<1>{version}\g<2>', s, count=1)
s, n2 = re.subn(r'("": \{\n\s+"name": "snet-desktop",\n\s+"version": ")[^"]+(")', rf'\g<1>{version}\g<2>', s, count=1)
if n1 != 1 or n2 != 1:
    raise SystemExit(f"cannot update {p}")
write(p, s)

p = "android/app-native/build.gradle"
s = read(p)
s, n = re.subn(r'versionName "[^"]+"', f'versionName "{version}"', s, count=1)
m = re.search(r'versionCode (\d+)', s)
if n != 1 or not m:
    raise SystemExit(f"cannot update {p}")
code = int(m.group(1))
if version != old_version:
    code += 1
s = re.sub(r'versionCode \d+', f'versionCode {code}', s, count=1)
write(p, s)

notes = pathlib.Path(f"docs/releases/v{version}.md")
if not notes.exists():
    notes.parent.mkdir(parents=True, exist_ok=True)
    notes.write_text(f"""# SNET v{version}

> Draft release notes. Verify all platform builds and update this file before
> tagging `v{version}`.

## Added

## Changed

## Fixed

## Compatibility

- Product version: `{version}`
- Coordination `APIVersion`: `1`

## Upgrade order

1. Coordination server (`snet-server`).
2. SDserver / NAS clients.
3. VPS client container.
4. Android and Docker Web clients.

## Known issues
""")
    notes.chmod(0o644)
    print(f"created {notes}")
print(f"bumped to {version} (android versionCode {code})")
PY

./scripts/check-version.sh
