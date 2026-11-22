# Contributing to Snet

Thanks for helping improve the Snet client.

## Scope

This repository covers the client, shared Web UI, desktop and Android apps,
Docker/NAS packaging, and OpenWrt firmware tooling. Server, relay, admin-server,
and deployment changes belong in the private `S-shidi/Snet-Server` repository.

## Branches and pull requests

1. Fork the repository.
2. Create a focused branch:
   ```bash
   git checkout -b fix/short-description
   ```
3. Make the smallest change that solves the problem.
4. Run the checks below.
5. Open a PR against `master` with:
   - what changed;
   - why it is needed;
   - how it was tested;
   - screenshots for UI changes.

Do not mix unrelated refactors, formatting changes, and behavior changes in one
PR.

Do not commit credentials, private keys, exported WireGuard configs, `.env`
files, signing keystores, or generated binaries.

## Checks

```bash
go test ./...
go vet ./...
(cd shared && go test ./...)
./scripts/build-web.sh
./scripts/build-android-web.sh
```

Cross-build the client CLI:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/client/...
```

## Code style

- Go: standard `gofmt`; keep exported APIs documented.
- TypeScript/CSS: follow the existing `shared/web` conventions.
- Prefer small, testable functions.
- Do not add new dependencies without a clear reason.

## Commit messages

Use a conventional prefix such as:

```text
fix(client): ...
feat(ui): ...
docs: ...
ci: ...
```

## Licensing

Client contributions are licensed under BSD-3-Clause. By contributing, you
agree that your changes are licensed under the same terms. See `LICENSE`.

The private server repository has separate contribution and licensing terms.
