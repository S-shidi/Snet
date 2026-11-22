# Security Policy

## Reporting a vulnerability

Do **not** open a public GitHub issue for security-sensitive reports.

Use GitHub Security Advisories:

https://github.com/S-shidi/Snet/security/advisories/new

Please include:

- affected client/platform and version;
- reproduction steps or a proof of concept;
- impact and any known mitigations;
- whether the issue is already public.

## Scope

High-value client areas include:

- WireGuard key handling and client config export;
- device identity and join approval on the client;
- local control API (`/ctl/*`) authentication;
- Docker/NAS packaging and Web UI exposure;
- Android/macOS/Windows privileged helper boundaries.

Server, relay, admin-server, and Cell operations are maintained in the private
server repository. Report server-side issues through the same advisory channel
and state that the issue is server-side.

## Supported versions

Security fixes are applied to the latest client release and `master`.

## Secrets

Never attach real `.env` files, private keys, tokens, QR codes, signing
keystores, or exported WireGuard configs to an issue, PR, or advisory. Redact
them first.
