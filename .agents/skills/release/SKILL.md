---
name: release
description: Release a new slop-police version to npm and GitHub, or fix a failed release. Use when bumping the version, publishing, adding a platform package, or debugging the release workflow.
---

# Release

1. Bump `version` in `npm/slop-police/package.json` and commit.
2. Push a matching tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. Watch `.github/workflows/release.yml` with `gh run watch`, then confirm with `npm view slop-police version`. npm shows a `0.0.0-stage` placeholder for a few minutes while it processes a new version.

The workflow publishes every platform package, then `slop-police`, then creates the GitHub release. It skips versions that are already on npm, so a release that failed halfway can be retried by moving the tag to the fixed commit: `git tag -f vX.Y.Z && git push -f origin vX.Y.Z`.

## npm access

Publishing uses npm trusted publishing (OIDC from GitHub Actions), not a token. Each package has a trusted publisher on npmjs.com (package Settings → Trusted Publisher → GitHub Actions, `kubk/slop-police`, `release.yml`, with "Allow npm publish" checked). npm asks for a 2FA code when saving it.

Trusted publishing can't create a package. A new package, such as a new platform, needs its first publish with a short-lived granular token, then its own trusted publisher.

npm's spam filter rejected the name `slop-police-win32-arm64`, probably because of the digits, so keep new package names close to the existing `slop-police-<os>-<cpu>` ones.
