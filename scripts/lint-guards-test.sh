#!/usr/bin/env bash
# Tests web/scripts/production-imports.cjs on a fixture tree: every way a
# source file can import a package must be seen, and nothing that does not
# ship may count. Then tests scripts/lib/eslint-report.py on fixture reports.
# scripts/lint-guards.sh runs this before the real scan.
#
#   scripts/lint-guards-test.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
scan="$root/web/scripts/production-imports.cjs"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
failures=0
has()   { if printf '%s\n' "$out" | grep -qF -- "$1"; then echo "  ok    $2"; else echo "  FAIL  $2 (output lacks: $1)"; failures=$((failures + 1)); fi; }
lacks() { if printf '%s\n' "$out" | grep -qF -- "$1"; then echo "  FAIL  $2 (output has: $1)"; failures=$((failures + 1)); else echo "  ok    $2"; fi; }

mkdir -p "$T/src/__tests__" "$T/src/test" "$T/src/deep"
cat > "$T/package.json" <<'JSON'
{ "dependencies": { "react": "1", "@scope/ok": "1" },
  "devDependencies": { "dev-default": "1", "dev-side": "1", "dev-export": "1", "dev-dynamic": "1", "dev-require": "1",
                       "dev-css": "1", "@scope/dev-sub": "1", "dev-type-only": "1", "dev-inline-type": "1",
                       "dev-in-test": "1", "dev-in-tests-dir": "1", "dev-in-comment": "1", "dev-in-string": "1",
                       "dev-after-glob": "1", "dev-css-comment": "1", "dev-in-dts": "1" } }
JSON
cat > "$T/src/a.tsx" <<'TS'
import React from "react"
import ok from "@scope/ok/sub"
import x from "dev-default"
import "dev-side"
export * from "dev-export"
import local from "./local"
import alias from "@/lib/x"
import fs from "node:fs"
import type { T } from "dev-type-only"
import { type U } from "dev-inline-type"
// import q from "dev-in-comment"
/* import "dev-in-comment" */
const s = 'import z from "dev-in-string"'
const accept = "image/*"
const later = () => import("dev-dynamic")
const r = require("dev-require")
import sub from "@scope/dev-sub/deep/path"
export const y = [x, ok, local, alias, fs, s, accept, later, r, sub, React]
TS
printf '@import "dev-css";\n/* @import "dev-css-comment"; */\n@import url("react/dist.css");\n' > "$T/src/style.css"
printf 'import a from "dev-in-test"\n' > "$T/src/b.test.ts"
printf 'import a from "dev-in-tests-dir"\n' > "$T/src/__tests__/c.ts"
printf 'import a from "dev-in-tests-dir"\n' > "$T/src/test/setup.ts"
printf 'import a from "dev-in-dts"\n' > "$T/src/types.d.ts"
printf 'import a from "dev-after-glob"\nexport default a\n' > "$T/src/deep/d.ts"

echo "the scan sees every form of import and nothing that does not ship"
set +e
out="$(cd "$root/web" && node "$scan" "$T/src" "$T/package.json" 2>&1)"; got=$?
set -e
[ "$got" = 1 ] && echo "  ok    exits 1 on a devDependency import" || { echo "  FAIL  exit $got, wanted 1:"; printf '%s\n' "$out" | sed 's/^/        /'; failures=$((failures + 1)); }
for pkg in dev-default dev-side dev-export dev-dynamic dev-require dev-css @scope/dev-sub dev-after-glob; do
  has "  $pkg  <- " "$pkg is seen"
done
for pkg in react @scope/ok dev-type-only dev-inline-type dev-in-test dev-in-tests-dir dev-in-comment dev-in-string dev-css-comment dev-in-dts; do
  lacks "  $pkg  <- " "$pkg does not count"
done
has "src/a.tsx" "names the importing file"

echo "a clean tree passes"
mkdir -p "$T/clean/src"
printf '{ "dependencies": { "react": "1" } }\n' > "$T/clean/package.json"
printf 'import React from "react"\nexport default React\n' > "$T/clean/src/a.ts"
set +e
out="$(cd "$root/web" && node "$scan" "$T/clean/src" "$T/clean/package.json" 2>&1)"; got=$?
set -e
[ "$got" = 0 ] && echo "  ok    exits 0" || { echo "  FAIL  exit $got, wanted 0: $out"; failures=$((failures + 1)); }
has "every package" "says so"

echo "the eslint report fails on any problem, architectural rules first"
report="$root/scripts/lib/eslint-report.py"
lint() { set +e; out="$(printf '%s' "$1" | RULE=no-restricted-syntax python3 "$report" 2>&1)"; got=$?; set -e; }
lint '[{"filePath":"/x/web/src/a.tsx","messages":[]}]'
[ "$got" = 0 ] && echo "  ok    a clean report exits 0" || { echo "  FAIL  clean report exit $got: $out"; failures=$((failures + 1)); }
lint '[{"filePath":"/x/web/src/a.tsx","messages":[{"ruleId":"react-hooks/exhaustive-deps","severity":1,"line":3,"column":4,"message":"missing dep"}]}]'
[ "$got" = 1 ] && echo "  ok    a warning exits 1" || { echo "  FAIL  warning exit $got, wanted 1"; failures=$((failures + 1)); }
has "web/src/a.tsx:3:4  [react-hooks/exhaustive-deps]" "names the file, line and rule"
lint '[{"filePath":"/x/web/src/a.tsx","messages":[{"ruleId":"no-unused-vars","severity":2,"line":1,"column":1,"message":"unused"},{"ruleId":"no-restricted-syntax","severity":2,"line":2,"column":1,"message":"raw button"}]}]'
[ "$got" = 1 ] && echo "  ok    an error exits 1" || { echo "  FAIL  error exit $got, wanted 1"; failures=$((failures + 1)); }
first="$(printf '%s\n' "$out" | grep -m1 -o '\[[a-z-]*\]$' || true)"
[ "$first" = "[no-restricted-syntax]" ] && echo "  ok    architectural rule listed first" || { echo "  FAIL  first listed: $first"; failures=$((failures + 1)); }

if [ "$failures" -gt 0 ]; then echo "lint-guards-test: $failures failure(s)"; exit 1; fi
echo "lint-guards-test: all passed"
