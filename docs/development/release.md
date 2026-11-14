# Release process

Release Please creates one release PR for the repository. Review its version and changelog, approve the CI run when GitHub requests approval, and merge the PR manually. Authentication uses only GITHUB_TOKEN.

## Publication

Release control runs on main pushes, a half-hourly schedule, and manual dispatch. It calls the distribution workflow with the release PR merge SHA, tag, and existing draft release ID. GoReleaser builds Linux and macOS archives for amd64 and arm64 and attaches them with SHA-256 checksums. It preserves the Release Please notes and leaves the release in draft.

The container workflow stages ghcr.io/moepig/fmlocal with the bare version tag. It verifies linux/amd64 and linux/arm64 and the source commit before advancing latest. Existing versioned images are reused when their version and commit match. A mismatch stops publication. Prereleases do not advance latest.

After every expected archive and checksum has been verified, release control publishes the draft. A failed run resumes the same draft and version. Tags are never moved for recovery.

## Operations

See [dependency and release operations](../dependency-release.md) for settings, holds, and recovery. The tools/release.sh helper requests reconciliation of main; versions are selected by Release Please from conventional commits.

Configuration is in [Release Please](../../release-please-config.json), the [manifest](../../.release-please-manifest.json), [GoReleaser](../../.goreleaser.yaml), and the [distribution workflow](../../.github/workflows/distribute.yml).
