#!/bin/bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version>"
  echo "  version: semantic version without v prefix (e.g. 1.2.3)"
  echo ""
  echo "Creates and pushes the vX.Y.Z tag. The Release workflow then runs"
  echo "GoReleaser, which builds the binaries, publishes the GitHub Release"
  echo "with generated notes, and pushes the container image to GHCR."
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

echo "==> Done. The Release workflow will run GoReleaser to publish the"
echo "    GitHub Release and container image for $TAG."
