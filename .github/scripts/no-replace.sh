#!/usr/bin/env bash
# Fails when the go.mod in the current directory has a replace directive.
#
# A consumer ignores replace; this repository's builds honour it, with or
# without a workspace. A released module carrying one was tested against code
# its consumers never get — against ../ even when the core version it
# requires does not exist (#116). Without one, a GOWORK=off build resolves
# every requirement from the module proxy, as a consumer does.
#
#   (cd redisstore && ../.github/scripts/no-replace.sh)
set -euo pipefail
replaces=$(go mod edit -json | jq -r '.Replace // [] | .[] | "\(.Old.Path) => \(.New.Path) \(.New.Version // "")"')
if [ -n "$replaces" ]; then
  echo "::error::go.mod has replace directives, which consumers ignore:"
  printf '%s\n' "$replaces"
  exit 1
fi
echo "no replace directives"
