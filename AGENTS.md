# SNET Agent Guide

This file is the session entry point for agents working on SNET.

## Start here

1. Read `docs/PROJECT-ATLAS.md` before multi-file work or architecture decisions.
2. Load one matching skill from `.opencode/skills/snet-*`:
   `snet-go-core`, `snet-desktop-tauri`, `snet-web-ui`,
   `snet-deploy-ops`, or `snet-project-atlas`.
3. Run `./scripts/project-audit.sh` to check worktree, package list, key files,
   route counts, archify links, and file modes.

## Non-negotiables

- Code and tests outrank stale docs. Verify before asserting.
- Do not expose tokens, pairing codes, private keys, or `ctl-token`.
- Do not commit unless explicitly asked.
- UI talks only to local `/ctl/*`; the Rust desktop layer must not implement networking.
- `shared/web` changes require `./scripts/build-web.sh`.
- Android Go↔Kotlin JSON contracts and `snetbind` exports must change together.
- Keep the fixed server address, port plan, lock rules, and bbolt persistence rules.
- Keep `VERSION` and release tags in sync (`v$(cat VERSION)`); follow `docs/RELEASE.md`.

## Memory maintenance

After any structural change (package, route, port, bucket, config field, platform
contract, build step), update `docs/PROJECT-ATLAS.md`, fix the affected domain skill,
append a changelog row, bump `last_verified`, and rerun `./scripts/project-audit.sh`.
If a fact is unverified, write `待验证` and the command that would verify it.
