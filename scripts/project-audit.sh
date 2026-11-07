#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

GO=${GO:-}
if [ -z "$GO" ]; then
	if command -v go >/dev/null 2>&1; then
		GO=go
	elif [ -x /tmp/opencode/golang/go/bin/go ]; then
		GO=/tmp/opencode/golang/go/bin/go
	fi
fi

say() { printf '%s\n' "$*"; }
count() {
	c=$(grep -c "$1" "$2" 2>/dev/null || true)
	printf '%s' "${c:-0}"
}

missing=0
check() {
	if [ ! -e "$1" ]; then
		say "MISSING  $1"
		missing=$((missing + 1))
	fi
}

say "== SNET project audit =="
say "date: $(date +%F)"

if git rev-parse --git-dir >/dev/null 2>&1; then
	say "commit: $(git rev-parse --short HEAD) ($(git log -1 --format=%ad --date=short))"
	say "branch: $(git branch --show-current 2>/dev/null || echo detached)"
	say "worktree entries: $(git status --porcelain | wc -l | tr -d ' ')"
else
	say "git: not a repository"
fi

say ""
say "== key paths =="
for f in \
	go.mod \
	VERSION \
	AGENTS.md \
	docs/PROJECT-ATLAS.md \
	docs/RELEASE.md \
	docs/DEPLOYMENTS.md \
	DESIGN.md \
	README.md \
	internal/server/server.go \
	internal/server/store.go \
	internal/server/relay.go \
	internal/client/daemon.go \
	internal/client/ctl.go \
	internal/client/control.go \
	internal/client/natmgr.go \
	internal/buildinfo/buildinfo.go \
	internal/protocol/types.go \
	snetbind/snetcore.go \
	android/app-native/src/main/kotlin/com/snet/app/SnetVpnService.kt \
	android/app-native/src/main/kotlin/com/snet/app/SnetBridge.kt \
	deploy/docker/web-build/web.ts \
	shared/web/types.ts \
	shared/web/ui.ts \
	scripts/build-web.sh \
	scripts/build-release.sh \
	scripts/bump-version.sh \
	scripts/check-version.sh \
	.github/workflows/ci.yml \
	.github/workflows/release.yml; do
	check "$f"
done

for f in \
	.archify/architecture-snet-product-20261020-162140/snet-product-architecture.html \
	.archify/workflow-snet-user-lifecycle-20261022-143213/snet-user-lifecycle.html \
	.archify/architecture-snet-lifecycle-20261020/snet-join-lifecycle.html \
	.archify/architecture-snet-nat-path-20261020/snet-nat-path.html \
	.archify/architecture-snet-bbolt-erd-20261020/snet-bbolt-erd.html; do
	check "$f"
done

say ""
say "== skills =="
if [ -d .opencode/skills ]; then
	find .opencode/skills -name SKILL.md -type f | sort |
		sed 's#^.opencode/skills/##; s#/SKILL.md$##' |
		grep '^snet-' | sed 's/^/  - /' || true
else
	say "  (no .opencode/skills directory)"
fi

say ""
say "== route registrations =="
say "  server: $(count 'mux.HandleFunc' internal/server/server.go)"
say "  ctl:    $(count 'mux.HandleFunc' internal/client/ctl.go)"

say ""
say "== Go =="
if [ -n "$GO" ]; then
	say "  toolchain: $("$GO" version 2>/dev/null || echo unknown)"
	"$GO" list ./... 2>&1 | sed 's/^/  /' || true
else
	say "  go not found; set GO=/path/to/go"
fi

say ""
say "== atlas freshness =="
if [ -f docs/PROJECT-ATLAS.md ]; then
	grep -E '^(last_verified|verified_code_commit|state):' docs/PROJECT-ATLAS.md |
		sed 's/^/  /' || true
	base=$(sed -n 's/^verified_code_commit:[[:space:]]*//p' docs/PROJECT-ATLAS.md | head -n 1)
	head_commit=$(git rev-parse --short HEAD 2>/dev/null || true)
	if [ -n "$base" ] && [ -n "$head_commit" ] && [ "$base" != "$head_commit" ]; then
		changed=$(git diff --name-only "$base"..HEAD -- \
			internal cmd snetbind android desktop shared go.mod go.sum 2>/dev/null |
			head -n 5 || true)
		if [ -n "$changed" ]; then
			say "  WARN: code changed since verified_code_commit ($base); re-verify and update atlas."
			printf '%s\n' "$changed" | sed 's/^/    /'
		else
			say "  HEAD differs from verified_code_commit only outside code paths."
		fi
	fi
else
	say "  missing docs/PROJECT-ATLAS.md"
fi

if git check-ignore -q .opencode/skills/snet-project-atlas/SKILL.md 2>/dev/null; then
	say "  note: .opencode/skills is gitignored; docs/PROJECT-ATLAS.md is the durable copy."
fi

say ""
say "== worktree hygiene =="
if git rev-parse --git-dir >/dev/null 2>&1; then
	code_dirty=$(git status --porcelain -- \
		internal cmd snetbind android desktop shared go.mod go.sum |
		wc -l | tr -d ' ')
	say "  code changes in worktree: $code_dirty"
	if [ "$code_dirty" -gt 0 ]; then
		say "  WARN: code differs from verified_code_commit; re-verify before release."
	fi

	modes=$(git diff --summary | grep -c 'mode change' || true)
	say "  mode changes: ${modes:-0}"
	if [ "${modes:-0}" -gt 0 ]; then
		say "  WARN: unexpected file mode changes; restore before review."
	fi

	diffcheck=$(git diff --check 2>&1 || true)
	if [ -n "$diffcheck" ]; then
		say "  diff check: warnings"
		printf '%s\n' "$diffcheck" | sed 's/^/    /'
	else
		say "  diff check: clean"
	fi
fi

say ""
if [ "$missing" -gt 0 ]; then
	say "RESULT: $missing missing path(s)"
	exit 1
fi
say "RESULT: key paths present"
