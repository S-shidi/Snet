# AGENTS.md

## Release rules

- Every new client release must publish the Docker image to the VPS Registry before the release is considered complete:
  - `66.187.6.46:5000/snet-client:vX.Y.Z`
  - `66.187.6.46:5000/snet-client:latest`
- After pushing, verify the tags exist and update sdserver/NAS to the released tag.
- Do not announce a client release while the Registry only has an older `snet-client` image.
