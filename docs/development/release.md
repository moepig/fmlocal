# Release process

fmlocal uses [GoReleaser](https://goreleaser.com/) to build binaries, generate
release notes, publish GitHub Releases, and push container images to the GitHub
Container Registry (GHCR). Releases are cut by pushing a `vX.Y.Z` tag.

## How it works

### 1. Pushing a tag triggers the release

The `Release` workflow (`.github/workflows/release.yaml`) runs on any pushed tag
matching `v*`. It runs `goreleaser release --clean`, which:

1. cross-compiles the `fmlocal` binary for `linux` and `darwin` on `amd64` and
   `arm64`, embedding the version via `-ldflags "-X main.version=..."`
2. packages the binaries as `tar.gz` archives plus a `checksums.txt`
3. generates release notes from the commits since the previous tag
4. publishes a GitHub Release with the archives and notes
5. builds a multi-platform image (`linux/amd64`, `linux/arm64`) from
   `Dockerfile.goreleaser` and pushes it to GHCR

```
ghcr.io/<owner>/fmlocal:1.2.3
ghcr.io/<owner>/fmlocal:latest
```

The image tag and embedded binary version are bare numbers (`1.2.3`); GoReleaser
strips the `v` prefix automatically via `{{ .Version }}`.

### 2. Changelog grouping

GoReleaser groups the generated notes by commit prefix (see
`.goreleaser.yaml`):

| Group | Matches |
|---|---|
| Features | `feat:` |
| Bug fixes | `fix:` |
| Dependencies | `chore(deps):`, `build(deps):` |
| Others | anything not excluded |

`docs:`, `test:`, `ci:`, `style:`, `refactor:`, and merge commits are excluded.
Unlike the previous release-please setup, the version is chosen by you (via the
tag), so dependency-update commits are included in the release notes without
needing to bump the version themselves.

## Making a release

Use the helper script, which creates and pushes the tag:

```sh
./tools/release.sh 1.2.3
```

Or push the tag manually:

```sh
git tag v1.2.3
git push origin v1.2.3
```

GoReleaser does the rest. There is no Release PR to merge and no manifest to
update — the tag is the single source of truth for the version.

## Versioning scheme

Versions follow [Semantic Versioning](https://semver.org/). Tags are prefixed
with `v` (`v1.2.3`). Pre-release tags (e.g. `v1.2.3-rc.1`) are published as
GitHub pre-releases automatically (`release.prerelease: auto`).

## Configuration files

| File | Purpose |
|---|---|
| `.goreleaser.yaml` | GoReleaser configuration (builds, archives, changelog, Docker) |
| `Dockerfile.goreleaser` | minimal image that copies the pre-built binary |
| `.github/workflows/release.yaml` | runs GoReleaser on pushed `v*` tags |
