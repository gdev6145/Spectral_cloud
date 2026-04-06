# Release Process

This repo publishes release artifacts on tags matching `v*`.

## Create a Release

```bash
git tag v1.2.3
git push origin v1.2.3
```

Artifacts created:
- `spectral-cloud-<os>-<arch>`
- `spectralctl-<os>-<arch>`
- `checksums.txt`
- `sbom.spdx.json`

## Optional Signing (cosign)

If you add `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` as GitHub Actions secrets, the workflow will sign `checksums.txt` and upload `checksums.txt.sig`.

Example:

```bash
cosign sign-blob --key cosign.key checksums.txt > checksums.txt.sig
```

Store `cosign.key` in `COSIGN_PRIVATE_KEY` and its password in `COSIGN_PASSWORD`.
