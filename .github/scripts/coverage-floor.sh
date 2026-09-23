#!/usr/bin/env bash
# Fails when the total statement coverage in a profile is below a floor
# (RNF-07). A file rather than inline YAML so it can be run, and seen failing,
# locally:
#
#   ./.github/scripts/coverage-floor.sh coverage.out 85
set -euo pipefail
profile=$1
floor=$2
total=$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $3); print $3 }')
if [ -z "$total" ]; then
  echo "::error::no total in $profile -- did the tests run?"
  exit 1
fi
echo "coverage: ${total}% (floor ${floor}%)"
if ! awk -v t="$total" -v f="$floor" 'BEGIN { exit !(t + 0 >= f + 0) }'; then
  echo "::error::coverage ${total}% is below the ${floor}% floor (RNF-07)"
  exit 1
fi
