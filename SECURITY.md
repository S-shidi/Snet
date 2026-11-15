# Security Policy

## Reporting a vulnerability

Do **not** open a public GitHub issue for security-sensitive reports.

Use GitHub's **Security Advisories**:

https://github.com/S-shidi/Snet/security/advisories/new

Please include:

- affected component and version;
- reproduction steps or a proof of concept;
- impact and any known mitigations;
- whether the issue is already public.

## Scope

High-value areas include:

- WireGuard key handling and client config export;
- relay ticket verification and relay routing;
- authentication, admin sessions and token handling;
- device identity and join approval;
- server-side request handling and rate limiting.

## Supported versions

Security fixes are applied to the latest release and `master`. Please test
against the latest `v1.1.x` release before reporting.

## Secrets

Never attach real `.env` files, private keys, tokens, QR codes or exported
WireGuard configs to an issue, PR or advisory. Redact them first.
