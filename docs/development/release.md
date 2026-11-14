# Release process

Release Please creates a release PR with the product version and changelog. A maintainer approves its CI run when GitHub requests approval, reviews compatibility, and merges it manually. The workflows use only GITHUB_TOKEN.

## Publication

The Release Please action runs on main pushes, a half-hourly schedule, or manual dispatch. For a merged release PR, it creates the version tag and a draft release, then passes its tag and SHA directly to distribute.yml. The distribution job checks that the checkout matches the tag and that the release is still a draft.

GoReleaser builds Linux and macOS archives for amd64 and arm64, attaches SHA-256 checksums, and publishes the versioned multi-platform container with its standard Docker configuration. It keeps the existing draft and Release Please notes. The workflow downloads the uploaded assets to verify their checksums before advancing latest and publishing the GitHub Release. Prereleases do not advance latest.

## Failure and retry

Rerun the failed job. If the original run is unavailable, run Release Please manually with tag set to the existing draft tag. Verify the tag, commit, and registry state before retrying: a retry may replace the versioned container. Published GitHub Releases are rejected before the build.

There is no custom action, release state issue, image ownership inspection, or automatic recovery service. Disable the Release Please workflow to pause publication and keep the release PR unmerged.

See [dependency and release operations](../dependency-release.md) for required settings. Configuration is in [Release Please](../../release-please-config.json), the [manifest](../../.release-please-manifest.json), [GoReleaser](../../.goreleaser.yaml), and the [distribution workflow](../../.github/workflows/distribute.yml).
