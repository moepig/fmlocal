# Release process

Release Please creates and updates a release PR using the repository's dedicated Automation App. A maintainer reviews the version, compatibility, changelog, and CI, then merges the PR manually. App merges trigger the normal main push workflow.

## Build and publication

Release Please creates the reviewed release. GoReleaser performs build and publication with GITHUB_TOKEN. Use immutable releases and investigate partial publication before issuing a new version. See the dependency and release operations document.

## Retry and recovery