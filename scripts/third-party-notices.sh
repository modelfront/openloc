#!/bin/sh
# Prints third-party license notices as markdown, one ecosystem at a time. The
# production images assemble their /app/THIRD_PARTY_NOTICES.md from these
# sections during the build (dockerfiles/*.dockerfile), so the file describes
# exactly the packages the image ships, and scripts/check-licenses.sh judges
# that file. Every package gets a `### name version` heading, a `License:`
# line and the license texts found in the package. POSIX sh: it runs unchanged
# in the Alpine build stages (sh, no bash).
#
#   third-party-notices.sh header <image>   the intro paragraph for that image
#   third-party-notices.sh go               the Go standard library and the modules
#                                           linked into ./cmd/server; run in the
#                                           module root, honours GOOS/GOARCH/CGO_ENABLED
#   third-party-notices.sh npm              production npm packages of the app in
#                                           the current directory (after npm ci);
#                                           ./licenses/<package>/ supplies the texts
#                                           of a package that ships none
#   third-party-notices.sh py               Python packages installed in python3
#
# For scripts/check-licenses.sh and its test:
#   third-party-notices.sh section <go|npm|py>   that ecosystem's `## ` heading
#   third-party-notices.sh label <file>...       the License: label those files get
#   third-party-notices.sh render                entries for the records on stdin
set -eu

nl='
'
tab="$(printf '\t')"
die() { echo "third-party-notices: $*" >&2; exit 1; }

header() {
  [ -n "${1:-}" ] || die "usage: third-party-notices.sh header <image>"
  cat <<EOF
# Third-party notices

OpenLoc is licensed under the Business Source License 1.1 (see \`LICENSE\` next to
this file). The \`$1\` image also ships the third-party packages listed below,
each under its own license, reproduced as found in the package. Where a package
offers a choice of licenses (\`A OR B\`), OpenLoc takes it under the first option
that is not a copyleft license, so \`MPL-2.0 OR Apache-2.0\` means Apache-2.0.
The operating system packages of the base image are not listed; the image's
package manager records their licenses.

EOF
}

section() {
  case "${1:-}" in
    go)  printf '## Server: Go modules linked into `cmd/server`\n\n' ;;
    npm) printf '## Frontend: npm packages bundled into the web app\n\n' ;;
    py)  printf '## Document parser: Python packages in the docparser image\n\n' ;;
    *)   die "usage: third-party-notices.sh section go|npm|py" ;;
  esac
}

# --- labels ------------------------------------------------------------------------
# gnu_label <upper-case text>: which GNU license a text is, by the title that
# comes first: every GPL text ends by naming the LGPL as an alternative, and
# the LGPL texts speak of the ordinary GPL throughout.
gnu_label() {
  best=${#1} label=""
  for t in "GNU AFFERO GENERAL PUBLIC LICENSE=AGPL-3.0" "GNU LESSER GENERAL PUBLIC LICENSE=LGPL" \
           "GNU LIBRARY GENERAL PUBLIC LICENSE=LGPL-2.0" "GNU GENERAL PUBLIC LICENSE=GPL"; do
    title="${t%%=*}"; before="${1%%"$title"*}"
    if [ ${#before} -lt "$best" ]; then best=${#before}; label="${t#*=}"; fi
  done
  case "$label" in
    LGPL) case "$1" in *"VERSION 3"*) echo "LGPL-3.0" ;; *) echo "LGPL-2.1" ;; esac ;;
    GPL)  case "$1" in *"VERSION 3"*) echo "GPL-3.0" ;; *) echo "GPL-2.0" ;; esac ;;
    "")   echo "see text" ;;
    *)    echo "$label" ;;
  esac
}

# A short label for the `License:` line, read from the whole text. Best
# effort: the full text follows, and that is what the licenses require. Terms
# that restrict use are looked for first, anywhere in the file and in any
# case, so a text that also quotes a permissive grant is not mistaken for it;
# MPL comes before the GNU family because its text names them. BSD variants
# differ by their clauses: the non-endorsement clause makes it 3-clause, the
# advertising clause 4-clause. ISC and 0BSD share their grant sentence; only
# ISC conditions it on keeping the notice.
license_label() {
  text="$(LC_ALL=C tr -s '[:space:]' ' ' < "$1")"
  upper="$(printf '%s' "$text" | LC_ALL=C tr '[:lower:]' '[:upper:]')"
  case "$upper" in
    *"COMMONS CLAUSE"*)                      echo "Commons Clause"; return ;;
    *"BUSINESS SOURCE LICENSE"*)             echo "BSL"; return ;;
    *"SERVER SIDE PUBLIC LICENSE"*)          echo "SSPL"; return ;;
    *"EUROPEAN UNION PUBLIC LICENCE"*)       echo "EUPL"; return ;;
    *"ELASTIC LICENSE"*)                     echo "Elastic"; return ;;
    *"MOZILLA PUBLIC LICENSE"*)
      case "$upper" in *"VERSION 2.0"*) echo "MPL-2.0" ;; *) echo "MPL" ;; esac; return ;;
    *"GENERAL PUBLIC LICENSE"*)              gnu_label "$upper"; return ;;
  esac
  case "$text" in
    *"Apache License"*)
      case "$text" in *"Version 2.0"*) echo "Apache-2.0" ;; *) echo "Apache" ;; esac ;;
    *"MIT License"*|*"Permission is hereby granted, free of charge"*) echo "MIT" ;;
    *"ISC License"*)                         echo "ISC" ;;
    *"Permission to use, copy, modify, and/or distribute"*)
      case "$text" in
        *"provided that the above copyright notice"*) echo "ISC" ;;
        *)                                            echo "0BSD" ;;
      esac ;;
    *"Redistribution and use in source and binary forms"*)
      case "$text" in
        *"advertising materials"*)            echo "BSD-4-Clause" ;;
        *"endorse or promote"*)               echo "BSD-3-Clause" ;;
        *)                                    echo "BSD-2-Clause" ;;
      esac ;;
    *"free and unencumbered software released into the public domain"*) echo "Unlicense" ;;
    *"CC0 1.0 Universal"*)                   echo "CC0-1.0" ;;
    *)                                       echo "see text" ;;
  esac
}

# files_label <files, one per line>: the labels of the license files, joined
# with " AND " when they differ, so a package with two licenses is judged on
# both. NOTICE and PATENTS files carry no grant and are skipped.
files_label() {
  printf '%s\n' "$1" | {
    seen="" joined=""
    while IFS= read -r f; do
      [ -s "$f" ] || continue
      case "$(basename "$f" | tr '[:upper:]' '[:lower:]')" in notice*|patents*) continue ;; esac
      l="$(license_label "$f")"
      printf '%s' "$seen" | grep -qxF -- "$l" && continue
      seen="$seen$l$nl"; joined="${joined:+$joined AND }$l"
    done
    printf '%s' "${joined:-none found}"
  }
}

# --- files -------------------------------------------------------------------------
# License files at the top level of a directory, one per line, sorted. Source
# files that happen to start with "notice" are not license files.
license_files() {
  find "$1" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
    ! -iname '*.go' ! -iname '*.js' ! -iname '*.ts' ! -iname '*.py' ! -iname '*.json' | sort
}

# utf8 <file>: the file as UTF-8. A few packages ship Latin-1 license files;
# where iconv exists they are converted, elsewhere copied as they are.
utf8() {
  if iconv -f UTF-8 -t UTF-8 "$1" >/dev/null 2>&1; then cat "$1"
  elif command -v iconv >/dev/null 2>&1; then iconv -f ISO-8859-1 -t UTF-8 "$1"
  else cat "$1"
  fi
}

# fence <file>: a run of backticks longer than any backtick-only line in the
# file as it is printed, trailing whitespace stripped (at least four), so the
# text cannot end its own fence.
fence() {
  n="$(utf8 "$1" | sed -e 's/[[:space:]]*$//' | grep -E '^`+$' | awk '{ if (length($0) > m) m = length($0) } END { print m + 0 }')"
  [ "$n" -ge 4 ] || n=3
  f=""; i=0
  while [ "$i" -le "$n" ]; do f="$f\`"; i=$((i + 1)); done
  printf '%s' "$f"
}

# --- records -----------------------------------------------------------------------
# Each ecosystem lists its packages as records, one per line:
#   name<TAB>version<TAB>label<TAB>origin[<TAB>license file]...
# label is the license the package declares, or "-" to read it from the files;
# origin says where a declared label comes from ("package.json", "metadata",
# "-" otherwise) or, as "repository", that the files are the source
# repository's because the package ships none. render turns records into the
# markdown entries.
record() {
  printf '%s\t%s\t%s\t%s' "$1" "$2" "$3" "$4"
  [ -z "${5:-}" ] || printf '%s\n' "$5" | while IFS= read -r f; do
    [ -z "$f" ] || printf '\t%s' "$f"
  done
  printf '\n'
}

render() {
  while IFS="$tab" read -r name version label origin files; do
    [ -n "$name" ] || continue
    [ "$label" != - ] || label="$(files_label "$(printf '%s' "$files" | tr '\t' '\n')")"
    printf '### %s %s\n\nLicense: %s\n\n' "$name" "$version" "$label"
    if [ -z "$files" ]; then
      case "$origin" in
        -) printf 'No license file in the package.\n\n' ;;
        *) printf 'No license file in the package; the license above is the one its %s declares.\n\n' "$origin" ;;
      esac
      continue
    fi
    [ "$origin" != repository ] \
      || printf 'The package ships no license file; the texts below are the license files of its source repository.\n\n'
    printf '%s\n' "$files" | tr '\t' '\n' | while IFS= read -r f; do
      [ -s "$f" ] || continue
      f_fence="$(fence "$f")"
      printf '`%s`:\n\n%s\n' "$(basename "$f")" "$f_fence"
      printf '%s\n' "$(utf8 "$f" | sed -e 's/[[:space:]]*$//')"
      printf '%s\n\n' "$f_fence"
    done
  done
}

go_records() {
  list="$(go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/server)"
  list="$(printf '%s\n' "$list" | sed '/^$/d' | sort -u)"
  [ -n "$list" ] || die "go list found no modules; run this in the module root"
  # The standard library and runtime are linked into the binary too.
  goroot="$(go env GOROOT)"
  files="$goroot/LICENSE"
  [ ! -f "$goroot/PATENTS" ] || files="$files$nl$goroot/PATENTS"
  record "Go standard library and runtime" "$(go env GOVERSION)" - - "$files"
  printf '%s\n' "$list" | while read -r path version dir; do
    [ -d "$dir" ] || die "module $path $version is not in the module cache"
    record "$path" "$version" - - "$(license_files "$dir")"
  done
}

npm_records() {
  [ -d node_modules ] || npm ci --ignore-scripts --no-audit --no-fund >/dev/null
  # npm ls exits non-zero on peer warnings it still lists through.
  list="$(npm ls --omit=dev --all --parseable 2>/dev/null || true)"
  [ -n "$list" ] || die "npm ls listed no packages; run this in the web app directory"
  printf '%s\n' "$list" | node -e '
const fs = require("fs"), path = require("path");
const dirs = fs.readFileSync(0, "utf8").split("\n").filter(Boolean);
const isFile = (d, f) => fs.statSync(path.join(d, f)).isFile();
const seen = new Set(), entries = [];
for (const d of dirs) {
  if (!d.includes("node_modules")) continue;                      // the web app itself
  const pj = JSON.parse(fs.readFileSync(path.join(d, "package.json"), "utf8"));
  const key = pj.name + "@" + pj.version;
  if (seen.has(key)) continue;
  seen.add(key);
  let lic = pj.license;
  if (lic && typeof lic === "object") lic = lic.type;
  if (!lic && Array.isArray(pj.licenses)) lic = pj.licenses.map(l => l.type || l).join(" OR ");
  let origin = lic ? "package.json" : "-";
  let files = fs.readdirSync(d)
    .filter(f => /^(licen[cs]e|copying|notice)/i.test(f) && !/\.(js|mjs|cjs|ts|json|map)$/i.test(f) && isFile(d, f))
    .sort().map(f => path.join(d, f));
  const own = path.resolve("licenses", pj.name);                 // texts for a package that ships none
  if (!files.length && fs.existsSync(own)) {
    files = fs.readdirSync(own).filter(f => isFile(own, f)).sort().map(f => path.join(own, f));
    origin = "repository";
  }
  entries.push({ key, name: pj.name, version: pj.version, lic: lic || "-", origin, files });
}
entries.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
for (const e of entries) process.stdout.write([e.name, e.version, e.lic, e.origin, ...e.files].join("\t") + "\n");'
}

py_records() {
  python3 - <<'PY'
import importlib.metadata as md, re
entries = []
for d in md.distributions():
    name = d.metadata["Name"]
    lic = (d.metadata.get("License-Expression") or "").strip()
    if not lic:
        cls = [c.split("::")[-1].strip() for c in (d.metadata.get_all("Classifier") or []) if c.startswith("License ::")]
        lic = " / ".join(cls) if cls else (d.metadata.get("License") or "").strip().split("\n")[0][:80]
    files = []
    for f in sorted(str(p) for p in (d.files or [])):
        if ".dist-info/" not in f:
            continue
        if re.search(r"(^|/)(licen[cs]e|copying|notice)", f.split(".dist-info/", 1)[1], re.I):
            files.append(str(d.locate_file(f)))
    entries.append((name.lower(), name, d.version, lic or "-", "metadata" if lic else "-", files))
for _, name, version, lic, origin, files in sorted(entries):
    print("\t".join([name, version, lic, origin, *files]))
PY
}

cmd="${1:-}"
if [ $# -gt 0 ]; then shift; fi
case "$cmd" in
  header)  header "$@" ;;
  section) section "$@" ;;
  label)   files=""; for f in "$@"; do files="$files$f$nl"; done; files_label "$files"; echo ;;
  render)  render ;;
  # The records are collected first so a failure in them fails the build;
  # a pipeline would only report the renderer's status.
  go)      records="$(go_records)";  section go;  printf '%s\n' "$records" | render ;;
  npm)     records="$(npm_records)"; section npm; printf '%s\n' "$records" | render ;;
  py)      records="$(py_records)";  section py;  printf '%s\n' "$records" | render ;;
  *)       die "usage: third-party-notices.sh header <image> | go | npm | py | section <eco> | label <file>... | render" ;;
esac
