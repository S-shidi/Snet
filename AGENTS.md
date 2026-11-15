# AGENTS.md

## Release rules

- Every new client release must publish the Docker image to the private Registry before the release is considered complete. The registry host and credentials are intentionally not stored in this repository.
  - `snet-client:vX.Y.Z`
  - `snet-client:latest`
- After pushing, verify the tags exist and update sdserver/NAS to the released tag.
- Do not announce a client release while the Registry only has an older `snet-client` image.
