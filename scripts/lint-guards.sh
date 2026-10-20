#!/usr/bin/env bash
# lint-guards.sh — fail on any frontend eslint problem, error or warning.
#
# `npm run lint` in web/ stays at zero problems. The `no-restricted-syntax`
# rules in web/eslint.config.js are listed first, because each one guards an
# invariant that produced real bugs:
#
#   - setActiveSegmentIdx      → lost editor drafts
#   - advanceTranslationInCache → missed post-save sync
#   - a raw <button>            → use the Button component
#   - <Badge> with color classes, or a hand-rolled pill → use <Chip>
#   - a reserved preset hue (violet/indigo) used freeform → name it in chip-presets.tsx
#
# It then fails when a file under web/src imports a package that is not a
# production dependency (web/scripts/production-imports.cjs): what the source
# imports ships, and only production packages reach the image's third-party
# notices.
#
# LIMIT OF THE CHIP RULES: they match one string literal at a time, so a pill
# whose shape and colour live in separate literals -- cn('rounded px-2', tint),
# or a colour read from a map or a named constant -- does not fire. A green run
# means "nobody pasted a pill", not "there are no hand-rolled pills". The
# reserved hues are the one fully-pinned case, by a source scan in
# web/src/components/ui/chip-lint.test.ts rather than by eslint.
#
# Exit 0 = no problems. Exit 1 = at least one (paths + messages printed).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/web"

RULE="no-restricted-syntax"

# The JSON report is read rather than eslint's exit status, so the
# architectural rules can be listed first. `|| true` keeps `set -e` from
# killing us on eslint's non-zero exit. stderr is kept separately: when eslint
# cannot even load its config (a bad selector regex, say) it prints nothing on
# stdout, and that must read as a broken config, not as a clean run.
errfile="$(mktemp)"
trap 'rm -f "$errfile"' EXIT
report="$(npx eslint . -f json 2>"$errfile" || true)"

if [ -z "$report" ]; then
  echo "lint-guards: eslint produced no JSON — it did not run. Its stderr:" >&2
  cat "$errfile" >&2
  exit 1
fi

printf '%s' "$report" | RULE="$RULE" python3 "$ROOT/scripts/lib/eslint-report.py"

# What web/src imports ships in the bundle, and the image's third-party
# notices list production dependencies only, so a package web/src imports
# must be in package.json "dependencies", not "devDependencies".
# web/scripts/production-imports.cjs reads the imports with TypeScript's
# parser; its own fixture test runs first.
bash "$ROOT/scripts/lint-guards-test.sh"
node scripts/production-imports.cjs src package.json
