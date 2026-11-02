#!/usr/bin/env bash
# Fails when the committed sqlc output in internal/database/sqlcgen is not what
# sqlc generates from the queries, or when the installed sqlc is not the
# version that generated it: another version rewrites every file's header, so
# its diff would fail for the wrong reason.
#
#   scripts/check-sqlc.sh [repo root]
set -uo pipefail

cd "${1:-$(dirname "${BASH_SOURCE[0]}")/..}" || exit 1
header=internal/database/sqlcgen/db.go

want="$(sed -n 's|^//   sqlc \(v.*\)$|\1|p' "$header" 2>/dev/null)"
if [ -z "$want" ]; then
  echo "check-sqlc: no sqlc version in the header of $header"
  exit 1
fi
have="$(sqlc version 2>/dev/null)"
if [ "$have" != "$want" ]; then
  echo "check-sqlc: sqlc $want generated internal/database/sqlcgen, but sqlc here is '${have:-not installed}': install $want"
  exit 1
fi
if ! sqlc diff; then
  echo "check-sqlc: internal/database/sqlcgen is stale: run 'make sqlc' and commit the result"
  exit 1
fi
echo "check-sqlc: internal/database/sqlcgen matches the queries (sqlc $want)"
