# Contributing to SNET

Thanks for helping improve SNET.

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

Do not mix unrelated refactors, formatting changes and behavior changes in one PR.
Do not commit credentials, private keys, exported WireGuard configs, `.env`
files or generated binaries.

## Checks

```bash
go test ./...
go vet ./...
```

Frontend:

```bash
./desktop-windows/frontend/build-frontend.sh
cd shared/web
npx --yes -p typescript@5.9.3 tsc --noEmit -p tsconfig.json
```

Cross-build the Windows client CLI:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

CI runs these checks for every pull request. External contributors do not need
write access to the repository.

## Code style

- Go: standard `gofmt`; keep exported APIs documented.
- TypeScript/CSS: follow the existing shared/web conventions.
- Prefer small, testable functions; add tests for server behavior.
- Do not add new dependencies without a clear reason.

## Commit messages

Use a conventional prefix such as:

```text
fix(server): ...
feat(ui): ...
docs: ...
ci: ...
```

## Licensing

Client components are BSD-3-Clause; server and relay components are
AGPL-3.0-only. By contributing, you agree that your changes are licensed under
the same component license. See [LICENSE](LICENSE).
