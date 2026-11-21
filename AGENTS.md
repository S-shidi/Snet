# AGENTS.md

## Documentation and layout

- Current architecture lives in `DESIGN.md` and `docs/PROJECT-ATLAS.md`; start from `docs/README.md`.
- Structure changes (new package, route, port, bbolt bucket, config field, platform contract) must update both files in the same change.
- Superseded docs go to `docs/archive/`; superseded clients or artifacts go to `archive/`. Do not leave stale content in `docs/` or the repository root.
- `dist/`, `dist-deploy/`, `bin/`, `node_modules/` and built Web assets are ignored artifacts, not source.

## Release rules

- Every new client release must publish the Docker image to the private Registry before the release is considered complete. The registry host and credentials are intentionally not stored in this repository.
  - `snet-client:vX.Y.Z`
  - `snet-client:latest`
- When server components change, publish the matching `snet-snetserv`, `snet-admin-server` and `snet-relay` images too; `scripts/install-server.sh` pulls them by tag.
- After pushing, verify the tags exist and update sdserver/NAS to the released tag.
- Do not announce a client release while the Registry only has an older `snet-client` image.
