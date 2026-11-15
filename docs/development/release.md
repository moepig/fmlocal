# Release process

Release Please creates and updates a release PR using the repository's dedicated Automation App. A maintainer reviews the version, compatibility, changelog, and CI, then merges the PR manually. App merges trigger the normal main push workflow.

## Build and publication

The release workflow creates a tag and draft, validates the merged App PR and final human approval, and builds the tagged SHA with read-only credentials. GoReleaser builds Linux and macOS binaries for amd64 and arm64. The build packages archives, checksums, and an OCI image archive without registry credentials.

The publication job uses GITHUB_TOKEN to attach release-manifest.json before adding any distribution files. Existing files and versioned image tags must match the recorded sizes and digests. It adds missing items without overwriting. It publishes the draft with make_latest=false, then advances Latest only for the greatest stable SemVer.

## Retry and recovery

Rerun a failed publication job when its build is still available. If outputs or the original run are unavailable, dispatch release-please.yml on main with the existing release_id. The workflow validates the release identity and artifact origin; rebuilt content must match the original publication record. A published release permits verification and Latest repair only.

See [dependency and release operations](../dependency-release.md) for prerequisites and shared operating procedures. Configuration is in [Release Please](../../release-please-config.json), the [manifest](../../.release-please-manifest.json), [GoReleaser](../../.goreleaser.yaml), the [release workflow](../../.github/workflows/release-please.yml), and the [distribution policy](../../.github/scripts/release-policy.json).
