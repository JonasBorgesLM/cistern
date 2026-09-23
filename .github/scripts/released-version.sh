#!/usr/bin/env bash
# Succeeds only for a released module version: fails for a pseudo-version,
# which names a commit rather than a release (RELEASING.md, step 3).
#
#   ./.github/scripts/released-version.sh v0.1.0
set -euo pipefail
v=${1:-}
if [[ ! "$v" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "::error::'$v' is not a module version"
  exit 1
fi
# The three pseudo-version forms all end in a 14-digit UTC timestamp and a
# 12-hex-digit commit hash: v0.0.0-TIMESTAMP-HASH, vX.Y.Z-pre.0.TIMESTAMP-HASH,
# vX.Y.(Z+1)-0.TIMESTAMP-HASH.
if [[ "$v" =~ [.-][0-9]{14}-[0-9a-f]{12}$ ]]; then
  echo "::error::the core is required at pseudo-version $v; require a released tag first"
  exit 1
fi
echo "core required at released version $v"
