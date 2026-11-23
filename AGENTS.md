# AGENTS.md

## Scope

- This repository contains the Snet client, shared Web UI, Android/macOS/Windows clients, Docker/NAS client packaging, and OpenWrt firmware tooling.
- Server, relay, admin-server, Cell deployment, and server operations live in the separate private `S-shidi/Snet-Server` repository. Do not add them here.
- Shared wire protocol code lives in the nested Go module `shared/` and must stay compatible with the private server.

## Layout

- Start from `README.md` and `docs/README.md`.
- `cmd/client/` contains `snetd` and `snetctl`.
- `internal/client/` contains the client daemon.
- `shared/protocol`, `shared/constants`, `shared/buildinfo` are a nested Go module (`github.com/S-shidi/snet/shared`).
- `shared/web/` is the shared Web UI; `deploy/docker/web-build/` is the esbuild entry used by Docker and firmware builds.
- `scripts/hardware/` builds OpenWrt images.
- Structure changes (new package, port, route, config field, platform contract) must update `README.md` and `docs/README.md` in the same change.

## Release rules

Follow `docs/RELEASING.md` for tags, GHCR images, and rollback.

- Client releases publish the Docker image to both GHCR and the private ACR registry before the release is complete:
  - `ghcr.io/s-shidi/snet-client:vX.Y.Z`
  - `<SNET_ACR_REGISTRY>/snet-client:vX.Y.Z`
  - `latest` in both registries
- Docker image tags are always `vX.Y.Z` (with the `v`). Never push a bare `X.Y.Z` image tag.
- Do not hardcode the ACR host: it comes from the `SNET_ACR_REGISTRY` secret.
- Do not reference private registries, private IPs, server deployment paths, or server secrets in this repository.
- Do not commit `.env` files, tokens, private keys, WireGuard exports, signing keystores, or generated binaries.

## Checks

```bash
go test ./...
go vet ./...
(cd shared && go test ./...)
./scripts/build-web.sh
```

Cross-build the client CLI when changing platform files:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/client/...
```

## Licensing

- Client components are BSD-3-Clause.
- Server components are separate proprietary software and are not part of this repository.
- Keep new client files compatible with the BSD-3-Clause license.
