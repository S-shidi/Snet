# Release Process

This repository owns the public client release train and the shared protocol module.

## Version train

| Artifact | Tag | Location |
|---|---|---|
| Shared protocol module | `shared/vX.Y.Z` | this repository |
| Public client | `vX.Y.Z` | this repository |
| Private server | `server/vX.Y.Z` | `S-shidi/Snet-Server` |

For a coordinated release, use the same numeric version across all three.
A client-only patch does not require a server release unless the shared protocol changed.

## Docker image tags

All Docker image tags are **`vX.Y.Z`** — the same string as the Git tag, with
the `v` prefix. Bare `X.Y.Z` image tags are no longer pushed, for any component.

| Artifact | Tag |
|---|---|
| Client image (GHCR) | `ghcr.io/s-shidi/snet-client:vX.Y.Z` |
| Client image (private registry) | `<SNET_ACR_REGISTRY>/snet-client:vX.Y.Z` |
| Server images | `snet-{snetserv,admin-server,relay}:vX.Y.Z` (both registries) |

Every image is pushed to **both** GHCR and the private ACR registry on each
release, with the same `vX.Y.Z` tag. The ACR side is configured by the
`SNET_ACR_REGISTRY`, `SNET_ACR_USERNAME` and `SNET_ACR_PASSWORD` repository
secrets and is skipped automatically when they are unset.

`latest` tracks the newest **stable** release in both registries. Prerelease
tags (`vX.Y.Z-rc1`) publish only the versioned image — never `latest` — and
skip the macOS, Windows, Android and firmware builds.

The release workflow also packages `Snet-vX.Y.Z-binaries.tar.gz` and the
WankeCloud firmware, and attaches them to the GitHub Release automatically;
no manual `gh release upload` is needed.

Self-hosted `compose.yaml` files must use the `v` prefix, e.g.
`snet-client:v1.2.4`. From `v1.2.4` on there is no backwards-compatible
`X.Y.Z` tag; see [`release-notes-v1.2.4.md`](release-notes-v1.2.4.md).

## Before tagging

```bash
go test ./...
(cd shared && go test ./...)
go vet ./...
./scripts/build-web.sh
./scripts/build-client-release.sh
```

- Confirm `VERSION` matches the intended tag.
- Confirm the public tree contains no server code, private registry, private IP, token, or deploy key.
- Confirm `shared/` is clean and public-ready.

## Shared module changes

If `shared/` changed:

```bash
git tag shared/vX.Y.Z
git push origin shared/vX.Y.Z
```

Then update the root `go.mod` to the new shared version and run `go mod tidy`.
The private server repository must bump the same shared version before its next release.

## Client release

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

This triggers:

- `Build Android App`
- `Build macOS App`
- `Build Windows App`
- `Release client image`
- `CI`

The release must include the expected platform assets and the GHCR image:

```text
ghcr.io/s-shidi/snet-client:vX.Y.Z
ghcr.io/s-shidi/snet-client:latest
```

If the GHCR package is private, set it to public in package settings before announcing.

## Release checklist

- [ ] `VERSION` matches the tag (CI fails the release if they differ)
- [ ] public CI green
- [ ] Android APK attached
- [ ] macOS DMG attached
- [ ] Windows installer/zip attached
- [ ] GHCR image tags exist and are pullable, and the version tag is `vX.Y.Z` (not `X.Y.Z`)
- [ ] public README/badges point to the right release
- [ ] no server/private material in the public tree
- [ ] server repository has bumped `shared` if needed

## Rollback

Do not move or delete a published tag. Publish a patch release instead.
Keep the previous GHCR tags until the next release is verified.
