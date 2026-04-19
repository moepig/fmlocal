#!/bin/bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version>"
  echo "  version: semantic version without v prefix (e.g. 1.2.3)"
  exit 1
}

[[ $# -ne 1 ]] && usage

VERSION="$1"
TAG="v${VERSION}"

if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: version must be in X.Y.Z format" >&2
  exit 1
fi

if git rev-parse "$TAG" &>/dev/null; then
  echo "Error: tag $TAG already exists" >&2
  exit 1
fi

echo "==> Creating tag $TAG"
git tag "$TAG"
git push origin "$TAG"

echo "==> Creating GitHub Release $TAG"
gh release create "$TAG" --title "$TAG" --generate-notes

echo "==> Updating .release-please-manifest.json"
echo "{ \".\": \"${VERSION}\" }" > .release-please-manifest.json
git add .release-please-manifest.json
git commit -m "chore: update release-please manifest to ${TAG}"
git push origin main

echo "==> Done. Container image will be built by the Release workflow."
