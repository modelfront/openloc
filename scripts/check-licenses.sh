#!/usr/bin/env bash
# Fails when a third-party package the production images ship has a license
# outside the allowlist below, or one whose license cannot be told, unless
# dependency-license-exceptions.toml accepts it. It builds both images for this machine's
# platform, reads /app/THIRD_PARTY_NOTICES.md out of each as the image's
# runtime user (which also proves the image carries the notices and LICENSE,
# readable), and judges every entry; a missing or empty ecosystem section is a
# failure, not a pass. Needs Docker. Bash 3.2 (macOS) is enough.
#
#   scripts/check-licenses.sh                  build the images and check them
#   scripts/check-licenses.sh <notices.md>...  check these files instead
#
# CHECK_LICENSES_EXCEPTIONS=<file> swaps the exceptions file. The file form,
# the variable and a fake `docker` on PATH are how scripts/check-licenses-test.sh
# drives this script.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$root"
generator="$root/scripts/third-party-notices.sh"
exceptions_file="${CHECK_LICENSES_EXCEPTIONS:-dependency-license-exceptions.toml}"
die() { echo "check-licenses: $*" >&2; exit 1; }

# Permissive licenses, as SPDX ids, plus MPL-2.0: its copyleft stops at the
# files it covers, which OpenLoc ships unmodified. "BSD" alone is what Python's
# classifier says for the 2- and 3-clause variants.
allowed="MIT MIT-0 MIT-CMU Apache-2.0 BSD-2-Clause BSD-3-Clause BSD ISC MPL-2.0
         0BSD CC0-1.0 OFL-1.1 PSF-2.0 Python-2.0 Unlicense Zlib BlueOak-1.0.0"

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
trim()  { local s="$1"; s="${s#"${s%%[![:space:]]*}"}"; printf '%s' "${s%"${s##*[![:space:]]}"}"; }

# The names packages use for the licenses above (Python's trove classifiers
# and common free-text spellings) → SPDX id. Anything else passes through.
canonical() {
  case "$(lower "$1")" in
    "mit license"|"the mit license"|expat)                       echo MIT ;;
    "mit no attribution license (mit-0)")                        echo MIT-0 ;;
    "apache software license"|"apache license 2.0"|"apache license, version 2.0"|"apache 2.0") echo Apache-2.0 ;;
    "bsd license"|"new bsd license"|"simplified bsd license")    echo BSD ;;
    "isc license (iscl)"|"isc license")                          echo ISC ;;
    "python software foundation license")                        echo PSF-2.0 ;;
    "mozilla public license 2.0 (mpl 2.0)")                      echo MPL-2.0 ;;
    "the unlicense (unlicense)"|"the unlicense")                 echo Unlicense ;;
    "zlib/libpng license"|"zlib license")                        echo Zlib ;;
    *)                                                           echo "$1" ;;
  esac
}

# id_allowed <label>: one license name, on the allowlist (case-insensitive).
id_allowed() {
  local id a; id="$(lower "$(canonical "$(trim "$1")")")"
  [ -n "$id" ] || return 1
  for a in $allowed; do [ "$id" = "$(lower "$a")" ] && return 0; done
  return 1
}

# label_allowed <label>: a single license, or an expression. " OR " offers a
# choice, and one option must be allowed. " AND ", "," and " / " join
# licenses that all apply, and every one must be allowed: the last two are not
# SPDX but come from Python classifier lists and free-text license fields,
# which do not promise a choice. Parentheses only group. A label with no
# license name in it is not allowed.
label_allowed() {
  local label parts part alts alt named=0
  label="$(trim "$1")"
  id_allowed "$label" && return 0
  label="$(printf '%s' "$label" | tr -d '()')"
  parts="${label// AND /$'\n'}"; parts="${parts//\//$'\n'}"; parts="${parts//,/$'\n'}"
  while IFS= read -r part; do
    [ -n "$(trim "$part")" ] || continue
    named=1
    alts="${part// OR /$'\n'}"
    while IFS= read -r alt; do
      id_allowed "$alt" && continue 2
    done <<< "$alts"
    return 1
  done <<< "$parts"
  [ "$named" = 1 ]
}

# --- the exceptions ----------------------------------------------------------------
# exceptions: the entries of the exceptions file as
# "package<TAB>license<TAB>expires<TAB>reason" lines; fails on a bad entry.
exceptions() {
  [ -f "$exceptions_file" ] || return 0
  awk -v file="$exceptions_file" '
    function fail(msg) { print "check-licenses: " file ": " msg > "/dev/stderr"; exit 1 }
    function flush() {
      if (!open) return
      if (pkg == "" || lic == "" || until == "" || reason == "") fail("entry " n " needs package, license, reason and expires")
      if (pkg !~ /^(go|npm|py):./) fail("entry " n ": package must be go:<name>, npm:<name> or py:<name>, as the notices print it")
      if (until !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) fail("entry " n ": expires must be YYYY-MM-DD")
      print pkg "\t" lic "\t" until "\t" reason
      pkg = lic = until = reason = ""; open = 0
    }
    function value() { sub(/^[^=]*=[[:space:]]*/, ""); gsub(/^"|"[[:space:]]*$/, ""); return $0 }
    { sub(/\r$/, "") }
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    /^\[\[Exceptions\]\]/ { flush(); open = 1; n++; next }
    /^package[[:space:]]*=/ { pkg = value(); next }
    /^license[[:space:]]*=/ { lic = value(); next }
    /^reason[[:space:]]*=/  { reason = value(); next }
    /^expires[[:space:]]*=/ { until = value(); next }
    { fail("line " NR ": unexpected line: " $0) }
    END { flush() }' "$exceptions_file"
}
# exception_for <ecosystem:name>: its "license<TAB>expires<TAB>reason", or nothing.
exception_for() { printf '%s\n' "$exception_list" | awk -F'\t' -v k="$1" '$1 == k { print $2 "\t" $3 "\t" $4; exit }'; }

# --- the notices -------------------------------------------------------------------
# entries <notices.md>...: "ecosystem<TAB>name<TAB>version<TAB>label" per package,
# from the `### name version` headings and `License:` lines outside the
# backtick fences; the generator's `## ` headings name the ecosystem. A fence
# ends only on the exact run of backticks that opened it, and every heading
# must have its License: line, so a text cannot hide the packages after it.
entries() {
  awk -v go="$("$generator" section go | head -n 1)" \
      -v npm="$("$generator" section npm | head -n 1)" \
      -v py="$("$generator" section py | head -n 1)" '
    /^````+$/ { if (fence == "") fence = $0; else if ($0 == fence) fence = ""; next }
    fence != "" { next }
    /^## / { eco = ""; if ($0 == go) eco = "go"; else if ($0 == npm) eco = "npm"; else if ($0 == py) eco = "py"; next }
    /^### / && eco != "" {
      entry = substr($0, 5); name = entry; version = ""
      if (match(entry, / [^ ]+$/)) { name = substr(entry, 1, RSTART - 1); version = substr(entry, RSTART + 1) }
      headings++; pending = 1; next
    }
    /^License: / && pending { print eco "\t" name "\t" version "\t" substr($0, 10); printed++; pending = 0 }
    END {
      if (headings != printed) {
        print "check-licenses: " headings " package headings but " printed " License: lines; the notices are malformed" > "/dev/stderr"
        exit 1
      }
    }' "$@"
}

# notices_from_image <tag> <dockerfile> <out>: build the image for this platform
# and copy its notices out as the runtime user, so an unreadable or missing
# file fails here.
notices_from_image() {
  echo "check-licenses: building $1 from $2 (cached layers make this quick after the first time)" >&2
  docker buildx build --load -t "$1" -f "$2" . >&2
  docker run --rm "$1" sh -c 'test -s /app/LICENSE && cat /app/THIRD_PARTY_NOTICES.md' > "$3" \
    || die "$1: /app/LICENSE or /app/THIRD_PARTY_NOTICES.md is missing or unreadable for the runtime user"
  [ -s "$3" ] || die "$1: /app/THIRD_PARTY_NOTICES.md is empty"
}

if [ $# -gt 0 ]; then
  for f in "$@"; do [ -f "$f" ] || die "no such notices file: $f"; done
  files=("$@")
else
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  notices_from_image openloc:check-licenses dockerfiles/openloc.dockerfile "$tmp/openloc.md"
  notices_from_image openloc-docparser:check-licenses dockerfiles/openloc-docparser.dockerfile "$tmp/docparser.md"
  files=("$tmp/openloc.md" "$tmp/docparser.md")
fi
labels="$(entries "${files[@]}")"
count() { printf '%s\n' "$labels" | grep -c "^$1	" || true; }
for eco in go npm py; do
  [ "$(count "$eco")" -gt 0 ] || die "the notices list no $eco packages: the section is missing or empty"
done

# --- the verdict -------------------------------------------------------------------
today="$(date +%F)"
exception_list="$(exceptions)"
flagged=() accepted=() used=""
while IFS=$'\t' read -r key lic until reason; do
  [ -n "$key" ] || continue
  [[ $until > $today ]] || flagged+=("  $key  exception expired on $until; remove it, or renew it after a new review")
done <<< "$exception_list"

while IFS=$'\t' read -r eco name version label; do
  [ -n "$name" ] || continue
  label_allowed "$label" && continue
  key="$eco:$name"
  IFS=$'\t' read -r exc_license until reason <<< "$(exception_for "$key")"
  if [ -z "$until" ]; then
    flagged+=("  $key $version  License: $label")
  elif [[ $until > $today ]] && [ "$exc_license" = "$label" ]; then
    used+="$key"$'\n'
    accepted+=("  $key $version  License: $label  (exception until $until: $reason)")
  elif [[ $until > $today ]]; then
    used+="$key"$'\n'
    flagged+=("  $key $version  License: $label  (its exception covers \"$exc_license\", not this)")
  else
    used+="$key"$'\n'
    flagged+=("  $key $version  License: $label  (its exception expired on $until)")
  fi
done <<< "$labels"

while IFS=$'\t' read -r key _; do
  [ -n "$key" ] || continue
  printf '%s' "$used" | grep -qxF -- "$key" \
    || echo "check-licenses: $exceptions_file names $key, which no image ships or whose license the allowlist already covers; remove the entry" >&2
done <<< "$exception_list"
summary="$(printf '%s\n' "$labels" | grep -c . || true) packages ($(count go) go, $(count npm) npm, $(count py) py)"
if [ "${#accepted[@]}" -gt 0 ]; then
  echo "check-licenses: accepted by $exceptions_file:"
  printf '%s\n' "${accepted[@]}"
fi
if [ "${#flagged[@]}" -gt 0 ]; then
  echo "check-licenses: ${#flagged[@]} of $summary need a license review:" >&2
  printf '%s\n' "${flagged[@]}" >&2
  cat >&2 <<EOF
A package stays out when its license is copyleft or cannot be told. Prefer
replacing the dependency. Otherwise, after a license review, add an exception
with the license as printed above, a reason and an expiry date to $exceptions_file.
EOF
  exit 1
fi
if [ "${#accepted[@]}" -gt 0 ]; then
  echo "check-licenses: $summary, ${#accepted[@]} accepted by $exceptions_file, the rest on the allowlist"
else
  echo "check-licenses: $summary, every license on the allowlist"
fi
