# Release process

Release Please uses the dedicated App to create a release PR. Review its version, changelog, compatibility, and CI before merging. The merge creates a tag and draft release; GoReleaser adds the distribution files and publishes that draft.

## Distribution

The release contains tar.gz archives for Linux and macOS on amd64 and arm64, plus checksums.txt. The ghcr.io/moepig/fmlocal image supports Linux on amd64 and arm64. Image tags contain the version; latest is not updated automatically.

GoReleaser builds and publishes in one job with the repository GITHUB_TOKEN. The App key remains in the separate Release Please job. PR CI runs unit tests, server E2E tests, and a snapshot release that builds archives and images without publishing them.

## Retry and recovery

Rerun a failed job only if it failed before writing distribution files or images. After a partial publication, inspect the draft, assets, and GHCR before proceeding. Publish a corrected version instead of overwriting existing assets. Immutable releases protect published GitHub assets and tags; GHCR tags are managed separately.

Configuration is in [GoReleaser](../../.goreleaser.yaml), [Release Please](../../release-please-config.json), and the [release workflow](../../.github/workflows/release-please.yml). See [dependency and release operations](../dependency-release.md) for prerequisites and stopping the workflows.
