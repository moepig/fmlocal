# Release process

fmlocal uses [release-please](https://github.com/googleapis/release-please) to automate versioning, changelog generation, and GitHub Releases. Container images are published to the GitHub Container Registry (GHCR) on every release.

## How it works

### 1. Commit on main triggers a Release PR

Every push to `main` runs the `Release Please` workflow (`.github/workflows/release-please.yaml`). release-please inspects commits since the last release and, if any are releasable (see below), opens or updates a Release PR that:

- bumps the version in `.release-please-manifest.json`
- appends an entry to `CHANGELOG.md`

The PR is kept up-to-date automatically as more commits land on `main`.

### 2. Merging the Release PR publishes the release

When the Release PR is merged, release-please:

1. creates a `vX.Y.Z` tag
2. publishes a GitHub Release with the generated release notes

### 3. The release triggers the container build

The `Release` workflow (`.github/workflows/release.yaml`) runs on `release: published`. It:

1. extracts the version from the tag (`v1.2.3` → `1.2.3`)
2. builds a multi-platform image (`linux/amd64`, `linux/arm64`) via `docker buildx`, passing `VERSION` as a build argument so the binary embeds it via `-ldflags`
3. pushes the image to GHCR with two tags: the exact version and `latest`

```
ghcr.io/<owner>/fmlocal:1.2.3
ghcr.io/<owner>/fmlocal:latest
```

## Commit message convention

release-please follows the [Conventional Commits](https://www.conventionalcommits.org/) spec to determine what kind of release to make.

| Prefix | Effect |
|---|---|
| `feat:` | minor version bump |
| `fix:` | patch version bump |
| `feat!:` / `BREAKING CHANGE:` | major version bump |
| `chore:`, `docs:`, `test:`, etc. | no release |

Commits that do not follow the convention are ignored for versioning purposes.

## Making a release

1. Land commits on `main` using conventional commit messages.
2. Wait for the Release PR to appear (or refresh if it already exists).
3. Review the generated `CHANGELOG.md` entry in the PR.
4. Merge the Release PR.

The tag, GitHub Release, and container image are created automatically.

## Manual release

Use this when you need to cut a release outside the normal release-please flow — for example, to ship a hotfix directly from a tag you pushed manually.

### Steps

1. Push the tag if it does not exist yet:

   ```sh
   git tag v1.2.3
   git push origin v1.2.3
   ```

2. Create the GitHub Release from the tag. This triggers the container build:

   ```sh
   gh release create v1.2.3 --title "v1.2.3" --notes "Hotfix: ..."
   ```

   The `Release` workflow fires on `release: published` and pushes the container image to GHCR as usual.

3. Update `.release-please-manifest.json` to match the version you just released, so release-please picks up from the right baseline:

   ```sh
   echo '{ ".": "1.2.3" }' > .release-please-manifest.json
   git commit -m "chore: update release-please manifest to v1.2.3"
   git push origin main
   ```

   Without this step, release-please will not know the manual release happened and may calculate the next version incorrectly.

### Behaviour of release-please after a manual release

release-please determines the next version by reading `.release-please-manifest.json`, not by inspecting existing tags. The table below shows what happens in each scenario after a manual release of `v1.2.3`:

| Manifest updated? | Next commit type | release-please proposes |
|---|---|---|
| Yes | `fix:` | `v1.2.4` |
| Yes | `feat:` | `v1.3.0` |
| No (still `1.2.2`) | `fix:` | `v1.2.3` — conflicts with the tag you already pushed; the Release PR will fail when release-please tries to create the tag again |

Always update the manifest after a manual release.

## Configuration files

| File | Purpose |
|---|---|
| `release-please-config.json` | release-please settings (release type: `go`) |
| `.release-please-manifest.json` | tracks the current released version |
| `.github/workflows/release-please.yaml` | runs release-please on push to main |
| `.github/workflows/release.yaml` | builds and pushes the container image on release |

## Versioning scheme

Versions follow [Semantic Versioning](https://semver.org/). Tags are prefixed with `v` (`v1.2.3`). The `v` prefix is stripped when passed to the Docker build so the image tag and the embedded binary version are bare numbers (`1.2.3`).
