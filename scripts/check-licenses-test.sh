#!/usr/bin/env bash
# Tests scripts/check-licenses.sh and the parts of scripts/third-party-notices.sh
# it relies on, on fixtures: a gate that cannot fail is no gate, so
# `make check-licenses` runs this first. No Docker and no Go toolchain: the
# check takes notices files as arguments or meets a fake `docker` on PATH, and
# the generator's Go section meets a fake `go`.
#
#   scripts/check-licenses-test.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
check="$root/scripts/check-licenses.sh"
gen="$root/scripts/third-party-notices.sh"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
failures=0
future="$(( $(date +%Y) + 1 ))-01-01"
today="$(date +%F)"

# run <expected exit> <exceptions file or -> <args...>: runs the check, keeps
# its combined output in $out and checks the exit code.
run() {
  local want="$1" exc="$2"; shift 2
  [ "$exc" != - ] || exc="$T/none.toml"
  set +e
  out="$(CHECK_LICENSES_EXCEPTIONS="$exc" "$check" "$@" 2>&1)"
  local got=$?
  set -e
  [ "$got" = "$want" ] || { echo "  FAIL  exit $got, wanted $want:"; printf '%s\n' "$out" | sed 's/^/        /'; failures=$((failures + 1)); }
}
has()   { if printf '%s\n' "$out" | grep -qF -- "$1"; then echo "  ok    $2"; else echo "  FAIL  $2 (output lacks: $1)"; failures=$((failures + 1)); fi; }
lacks() { if printf '%s\n' "$out" | grep -qF -- "$1"; then echo "  FAIL  $2 (output has: $1)"; failures=$((failures + 1)); else echo "  ok    $2"; fi; }
same()  { if [ "$1" = "$2" ]; then echo "  ok    $3"; else echo "  FAIL  $3 (got '$1', wanted '$2')"; failures=$((failures + 1)); fi; }
has_line() { if printf '%s\n' "$out" | grep -q -- "$1"; then echo "  ok    $2"; else echo "  FAIL  $2 (no line matching: $1)"; failures=$((failures + 1)); fi; }

# Fixtures go through the generator itself: entry renders one record, section
# prints the heading, so the test reads exactly what an image would print.
entry()   { printf '%s\t%s\t%s\t-%s\n' "$1" "$2" "$3" "${4:+	$4}" | "$gen" render; }
section() { "$gen" section "$1"; }
# exception <file> <package> <license> <expires> [reason]: appends one entry.
exception() {
  printf '\n[[Exceptions]]\npackage = "%s"\nlicense = "%s"\nreason = "%s"\nexpires = %s\n' "$2" "$3" "${5:-r}" "$4" >> "$1"
}

# A license text that tries to end the fence and inject entries, once as is
# and once with CRLF line ends and a trailing space, which rendering strips.
printf 'MIT License\n\nPermission is hereby granted, free of charge\n````\n## %s\n### smuggled 9.9\n\nLicense: GPL-3.0\n````\n' \
  "$(section npm | head -n 1 | cut -c4-)" > "$T/tricky-LICENSE"
awk '{ printf "%s \r\n", $0 }' "$T/tricky-LICENSE" > "$T/tricky-crlf-LICENSE"

{
  section go
  entry "Go standard library and runtime" go1.26.9 "BSD-3-Clause"
  entry github.com/good/mod v1.0.0 "MIT"
  entry github.com/tricky/mod v3.0.0 "-" "$T/tricky-LICENSE"
  entry github.com/trickier/mod v3.1.0 "-" "$T/tricky-crlf-LICENSE"
  entry github.com/dual/mod v2.0.0 "Apache-2.0 OR BSD-2-Clause"
  section npm
  entry dompurify 3.4.16 "(MPL-2.0 OR Apache-2.0)"
  entry victory-vendor 37.3.6 "MIT AND ISC"
  entry @scope/pkg 1.0.0 "BSD"
  section py
  entry python-docx 1.2.0 "MIT License"
  entry uvloop 0.23.0 "Apache Software License / MIT License"
  entry pillow 12.3.0 "MIT-CMU"
  entry pytesseract 0.3.13 "Apache Software License"
  entry pypdfium2 5.13.0 "BSD-3-Clause, Apache-2.0, dependency licenses"
} > "$T/good.md"

{
  section go
  entry github.com/good/mod v1.0.0 "MIT"
  entry github.com/copyleft/mod v1.0.0 "GPL-3.0"
  entry github.com/unknown/mod v1.0.0 "see text"
  entry github.com/nofile/mod v1.0.0 "none found"
  printf '### github.com/blank/mod v1.0.0\n\nLicense: \n\n'
  entry github.com/parens/mod v1.0.0 "()"
  section npm
  entry both-apply 1.0.0 "MIT AND GPL-2.0"
  entry a-choice 1.0.0 "MIT OR GPL-2.0"
  section py
  entry classified 1.0 "MIT License / GNU General Public License v3 (GPLv3)"
} > "$T/bad.md"

echo "permissive notices pass, including a text that tries to end its fence"
exception "$T/pypdfium2.toml" py:pypdfium2 "BSD-3-Clause, Apache-2.0, dependency licenses" "$future"
run 0 "$T/pypdfium2.toml" "$T/good.md"
has "13 packages (5 go, 3 npm, 5 py), 1 accepted" "counts every entry once, none inside a fence"
lacks "smuggled" "a fence is closed only by the run of backticks that opened it"
same "$(grep -c '^`````$' "$T/good.md")" 4 "the generator picks a longer fence for such texts, CRLF or not"

echo "copyleft, unknown and blank licenses fail, naming the packages"
run 1 - "$T/bad.md"
has "7 of 9 packages (6 go, 2 npm, 1 py)" "flags seven"
has "go:github.com/copyleft/mod v1.0.0  License: GPL-3.0" "GPL"
has "go:github.com/unknown/mod v1.0.0  License: see text" "a license the generator could not name"
has "go:github.com/nofile/mod v1.0.0  License: none found" "a package without a license file"
has_line '^  go:github.com/blank/mod v1.0.0  License: $' "a blank label"
has "go:github.com/parens/mod v1.0.0  License: ()" "a label with no license name"
has "npm:both-apply 1.0.0  License: MIT AND GPL-2.0" "AND needs every part"
has "py:classified 1.0  License: MIT License / GNU General Public License v3 (GPLv3)" "a classifier list needs every part"
lacks "npm:a-choice" "OR needs one part"
lacks "go:github.com/good/mod" "an allowed package is not named"
has "with the license as printed above, a reason and an expiry date" "says what to do"

echo "a reviewed exception accepts a package while its license and date hold"
exception "$T/exc.toml" go:github.com/copyleft/mod "GPL-3.0" "$future" "example = reviewed"
exception "$T/exc.toml" go:github.com/unknown/mod "MIT" "$future" "the label changed since the review"
exception "$T/exc.toml" npm:not-shipped "GPL-3.0" "$future"
run 1 "$T/exc.toml" "$T/bad.md"
has "accepted by $T/exc.toml:" "accepted entries are listed"
has "go:github.com/copyleft/mod v1.0.0  License: GPL-3.0  (exception until $future: example = reviewed)" "matching license and date accept"
has "go:github.com/unknown/mod v1.0.0  License: see text  (its exception covers \"MIT\", not this)" "a changed label is flagged again"
has "names npm:not-shipped, which no image ships" "an unused entry warns"

echo "an expired entry fails on its own, from its expiry date on"
exception "$T/expired.toml" py:pypdfium2 "BSD-3-Clause, Apache-2.0, dependency licenses" "$future"
exception "$T/expired.toml" go:github.com/good/mod "MIT" 2020-01-01
run 1 "$T/expired.toml" "$T/good.md"; has "1 of 13 packages" "only the expired entry fails"
has "go:github.com/good/mod  exception expired on 2020-01-01" "a past date"
exception "$T/today.toml" py:pypdfium2 "BSD-3-Clause, Apache-2.0, dependency licenses" "$future"
exception "$T/today.toml" go:github.com/good/mod "MIT" "$today"
run 1 "$T/today.toml" "$T/good.md"; has "exception expired on $today" "the expiry date itself"
exception "$T/unused.toml" npm:not-shipped "GPL-3.0" "$future"
exception "$T/unused.toml" py:pypdfium2 "BSD-3-Clause, Apache-2.0, dependency licenses" "$future"
run 0 "$T/unused.toml" "$T/good.md"; has "names npm:not-shipped, which no image ships" "an unused entry alone only warns"

echo "exceptions make a clean run"
for p in "go:github.com/copyleft/mod:GPL-3.0" "go:github.com/unknown/mod:see text" "go:github.com/nofile/mod:none found" \
         "npm:both-apply:MIT AND GPL-2.0" "py:classified:MIT License / GNU General Public License v3 (GPLv3)"; do
  exception "$T/exc2.toml" "${p%%:*}:$(printf '%s' "${p#*:}" | cut -d: -f1)" "${p##*:}" "$future"
done
grep -v -e blank -e parens "$T/bad.md" > "$T/bad2.md"
run 0 "$T/exc2.toml" "$T/bad2.md"; has "5 accepted by $T/exc2.toml, the rest on the allowlist" "says what was accepted"

echo "a bad exceptions file fails"
printf '[[Exceptions]]\npackage = "npm:a"\nreason = "r"\nexpires = %s\n' "$future" > "$T/exc3.toml"
run 1 "$T/exc3.toml" "$T/good.md"; has "entry 1 needs package, license, reason and expires" "a missing field"
printf '[[Exceptions]]\npackage = "npm:a"\nlicense = "X"\nreason = "r"\nexpires = 2030-1-1\n' > "$T/exc3.toml"
run 1 "$T/exc3.toml" "$T/good.md"; has "expires must be YYYY-MM-DD" "a bad date"
printf '[[Exceptions]]\npackage = "a"\nlicense = "X"\nreason = "r"\nexpires = %s\n' "$future" > "$T/exc3.toml"
run 1 "$T/exc3.toml" "$T/good.md"; has "package must be go:<name>, npm:<name> or py:<name>" "a package without its ecosystem"
printf 'garbage\n' > "$T/exc3.toml"
run 1 "$T/exc3.toml" "$T/good.md"; has "line 1: unexpected line: garbage" "a line that is not an entry"
awk '{ printf "%s\r\n", $0 }' "$T/pypdfium2.toml" > "$T/crlf.toml"
run 0 "$T/crlf.toml" "$T/good.md"; has "1 accepted" "a CRLF checkout of the file still parses"

echo "a missing or empty ecosystem section fails instead of passing"
{ section go; entry github.com/good/mod v1.0.0 "MIT"; section npm; entry a 1.0.0 "MIT"; } > "$T/nopy.md"
run 1 - "$T/nopy.md"; has "the notices list no py packages" "a missing section"
{ section go; entry github.com/good/mod v1.0.0 "MIT"; section npm; section py; } > "$T/emptynpm.md"
run 1 - "$T/emptynpm.md"; has "the notices list no npm packages" "an empty section"
{ cat "$T/good.md"; printf '### headless 1.0\n\nNo license line here.\n\n'; } > "$T/headless.md"
run 1 - "$T/headless.md"; has "package headings but" "a heading without its License: line is malformed, not skipped"
run 1 - "$T/missing.md"; has "no such notices file" "a missing file"

echo "the Go label is read from the whole text, restrictive terms first"
lic() { printf '%s' "$2" > "$T/lic-$1"; printf '%s\n' "$T/lic-$1"; }
mit="$(lic mit 'The MIT License. Permission is hereby granted, free of charge, to any person')"
same "$("$gen" label "$mit")" "MIT" "MIT"
same "$("$gen" label "$(lic apache 'Licensed under the Apache License, Version 2.0 (the "License")')")" "Apache-2.0" "Apache-2.0"
same "$("$gen" label "$(lic bsd2 'Redistribution and use in source and binary forms, with or without modification, are permitted')")" "BSD-2-Clause" "BSD-2-Clause"
same "$("$gen" label "$(lic bsd3 'Redistribution and use in source and binary forms ... may be used to endorse or promote products')")" "BSD-3-Clause" "BSD-3-Clause"
same "$("$gen" label "$(lic bsd4 'Redistribution and use in source and binary forms ... All advertising materials mentioning features ... endorse or promote')")" "BSD-4-Clause" "BSD-4-Clause"
same "$("$gen" label "$(lic isc 'Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted, provided that the above copyright notice and this permission notice appear in all copies.')")" "ISC" "ISC"
same "$("$gen" label "$(lic 0bsd 'Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted.')")" "0BSD" "0BSD"
same "$("$gen" label "$(lic gpl3 'GNU GENERAL PUBLIC LICENSE Version 3, 29 June 2007 ... If this is what you want to do, use the GNU Lesser General Public License instead of this License.')")" "GPL-3.0" "GPL-3.0, although it ends by naming the LGPL"
same "$("$gen" label "$(lic gpl2 'GNU GENERAL PUBLIC LICENSE Version 2, June 1991 ... use the GNU Library General Public License instead of this License.')")" "GPL-2.0" "GPL-2.0, although it ends by naming the Library GPL"
same "$("$gen" label "$(lic lgpl 'GNU Lesser General Public License, version 2.1, February 1999 ... the ordinary GNU General Public License')")" "LGPL-2.1" "LGPL in title case, although it speaks of the GPL"
same "$("$gen" label "$(lic agpl 'GNU AFFERO GENERAL PUBLIC LICENSE Version 3')")" "AGPL-3.0" "AGPL-3.0"
same "$("$gen" label "$(lic mpl 'Mozilla Public License Version 2.0 ... "Secondary License" means either the GNU General Public License, Version 2.0, the GNU Lesser General Public License, Version 2.1, the GNU Affero General Public License, Version 3.0')")" "MPL-2.0" "MPL-2.0, although it names the GNU licenses"
same "$("$gen" label "$(lic commons 'MIT License. Permission is hereby granted, free of charge ... "Commons Clause" License Condition v1.0')")" "Commons Clause" "a rider on a permissive grant"
same "$("$gen" label "$(lic late "$(printf 'Permission is hereby granted, free of charge, to any person. %6000s GNU General Public License version 3' '')")")" "GPL-3.0" "a copyleft text after 4000 bytes of permissive text"
gpl="$(lic copying 'GNU GENERAL PUBLIC LICENSE Version 2, June 1991')"
same "$("$gen" label "$mit" "$gpl")" "MIT AND GPL-2.0" "two license files give both labels"
mkdir -p "$T/nf"; printf 'Copyright 2020 Someone\n' > "$T/nf/NOTICE"; printf 'Additional IP Rights Grant\n' > "$T/nf/PATENTS"
same "$("$gen" label "$mit" "$T/nf/NOTICE" "$T/nf/PATENTS")" "MIT" "NOTICE and PATENTS do not label"
same "$("$gen" label "$(lic empty '')")" "none found" "an empty file is no license file"
same "$("$gen" label)" "none found" "no file at all"
same "$("$gen" label "$(lic odd 'All rights reserved. Contact the author.')")" "see text" "an unknown text"

echo "a failing Go listing fails the generator instead of leaving a short section"
mkdir -p "$T/bin" "$T/goroot" "$T/mod"
printf 'MIT License\nPermission is hereby granted, free of charge\n' > "$T/goroot/LICENSE"
printf 'MIT License\nPermission is hereby granted, free of charge\n' > "$T/mod/LICENSE"
cat > "$T/bin/go" <<'SH'
#!/bin/sh
case "$1 $2" in
  "list -deps")   printf 'example.com/mod v1.0.0 %s\n' "$FAKE_MOD_DIR" ;;
  "env GOROOT")   echo "$FAKE_GOROOT" ;;
  "env GOVERSION") echo go1.0.0 ;;
  *) exit 1 ;;
esac
SH
chmod +x "$T/bin/go"
set +e
out="$(PATH="$T/bin:$PATH" FAKE_MOD_DIR="$T/mod" FAKE_GOROOT="$T/goroot" "$gen" go 2>&1)"; got=$?
set -e
same "$got" 0 "a Go section renders"
has "### Go standard library and runtime go1.0.0" "the standard library comes first"
has "### example.com/mod v1.0.0" "then the modules"
same "$(printf '%s\n' "$out" | grep -c '^License: MIT$')" 2 "with their labels"
set +e
out="$(PATH="$T/bin:$PATH" FAKE_MOD_DIR="$T/missing" FAKE_GOROOT="$T/goroot" "$gen" go 2>&1)"; got=$?
set -e
same "$got" 1 "a module missing from the cache fails the generator"
lacks "### Go standard library" "and prints no partial section"

echo "the Docker path reads both images as the runtime user"
mkdir -p "$T/images"
cat > "$T/bin/docker" <<'SH'
#!/usr/bin/env bash
case "$1" in
  buildx) exit 0 ;;
  run)    [ -z "${FAKE_DOCKER_FAIL:-}" ] || exit 1; cat "$FAKE_DOCKER_DIR/$3.md" ;;
  *)      exit 1 ;;
esac
SH
chmod +x "$T/bin/docker"
{ section go; entry github.com/good/mod v1.0.0 "MIT"; section npm; entry a 1.0.0 "MIT"; } > "$T/images/openloc:check-licenses.md"
{ section py; entry pypdfium2 5.13.0 "BSD-3-Clause, Apache-2.0, dependency licenses"; } > "$T/images/openloc-docparser:check-licenses.md"
set +e
out="$(cd "$root" && PATH="$T/bin:$PATH" FAKE_DOCKER_DIR="$T/images" "$check" 2>&1)"; got=$?
set -e
same "$got" 0 "two images, the repo's own dependency-license-exceptions.toml"
has "accepted by dependency-license-exceptions.toml" "the committed pypdfium2 entry matches the label the image prints"
set +e
out="$(cd "$root" && PATH="$T/bin:$PATH" FAKE_DOCKER_DIR="$T/images" FAKE_DOCKER_FAIL=1 "$check" 2>&1)"; got=$?
set -e
same "$got" 1 "an image without readable LICENSE or notices fails"
has "missing or unreadable for the runtime user" "and says so"

if [ "$failures" -gt 0 ]; then echo "check-licenses-test: $failures failure(s)"; exit 1; fi
echo "check-licenses-test: all passed"
