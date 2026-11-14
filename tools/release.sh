#!/bin/bash
set -euo pipefail

if [[ $# -ne 0 ]]; then
  echo "Usage: $0" >&2
  echo "Review and merge the release PR; Release Please selects the version." >&2
  exit 1
fi

gh workflow run release-control.yml --ref main -f operation=reconcile
echo "Release reconciliation requested. Review the Release control workflow run."
