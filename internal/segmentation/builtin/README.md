# Built-in segmentation rule sets

One JSON file per set, embedded in the server and read-only. The file name is
the id without `builtin:` (`en@1.json` is `builtin:en@1`). A released file never
changes, because files imported with it keep a copy of its rules and the
catalog must keep describing them; `builtin_test.go` pins each file's SHA-256.
To improve a set, add the next version (`en@2.json`), pin it, and leave the old
one in place.

## The sets

- `conservative@1` — the rules every file stored without a segmentation
  snapshot is parsed with. A break needs a terminator (`. ! ? :`), whitespace,
  and a capital, a digit or an opening bracket or quote. A single lowercase
  letter before a full stop (`e.g.`, `i.e.`) and 13 abbreviations, matched
  case-insensitively, never end a sentence. It matches the segmentation of
  `internal/parser/testdata/st4_reference.xlf`, so a TM built with that tool
  leverages against our segments. The colon is a terminator because that
  reference splits on it; the whitespace requirement keeps `I:O`, `1:2` and
  `ISO 4064-2-55:2018` whole. Unit abbreviations (`ms`, `min`, `no`) are not
  abbreviations here: in technical text a unit far more often ends a sentence.
  `internal/parser/sentence_oracle_test.go` keeps the splitter these rules
  reproduce, and the tests there prove the two split every text alike.
- `default@1` — "Default", for any language: Western terminators followed by whitespace,
  full-width `。！？｡` that end a sentence without a space (not when a closing
  bracket or quote follows), and the Arabic, Urdu and Devanagari terminators.
- `en@1`, `de@1`, `es@1`, `fr@1`, `it@1`, `pt@1`, `ru@1` — a single lowercase
  letter before a full stop never ends a sentence; `. ! ? …`, with any closing
  brackets and quotes, end one when whitespace and a capital, a digit or an
  opening bracket or quote follow; full-width terminators as in `default@1`.
  The colon never ends a sentence. `de@1` adds that a number of up to three
  digits before a full stop and a word is an ordinal (`am 3. Oktober`).
- `th@1` — paragraph mode: Thai has no sentence-ending punctuation.

## Abbreviation lists

The language sets' abbreviation lists are the Unicode CLDR sentence-break
suppressions (`cldr/*.xml`, copied from github.com/unicode-org/cldr
`common/segments/` at the commit named in `cldr/VENDORED`, Unicode License v3,
`cldr/LICENSE`), without their final full stop and curated:

- entries without a final full stop (data errors) are dropped;
- entries that are ordinary words and end sentences, and so would merge two
  sentences, are dropped: in `en` `OK`, `Ok`, `Joe`, `Act`, `Job`, `Link`,
  `Long`, `Hat`, `By`, `Up`, `To`, `Do`, `Is`, `On`, `Go`, `As`, `In`, `Or`,
  `All`, `Card`, `Pro`, `Lib`, `Def`, `Diff`, `AA`, `AB`, `AD`, `DC`, `PC`,
  `A`, `I`, `Ed`, `Ex` (and `Op`, which moves to the number list); in `de`
  `Port`, `Akt`, `App`, `Max`, `Test` (and `Art`, which moves to the number
  list); in `es` `mar`, `Bien`, `Sol`, `Kit`, `All`, `Var`, `RAM`; in `fr` `mer`, `jeu`, `All`, `Var`, `Desc`, `Comm`,
  `Mart`; in `it` `all`, `on`; in `pt` `mar`, `com`;
- `en` adds common lowercase abbreviations (`e.g`, `i.e`, `etc`, `vs`, `cf`,
  `al`, `approx`, …), titles (`Dr`, `Jr`, `Sr`, `St`, …) and the month and
  weekday abbreviations CLDR lacks.

The number-abbreviation lists (`No. 5`, `Fig. 3`, `p. 12`) are OpenLoc's own.
`cldr/VENDORED` makes `scripts/third-party-notices.sh` list the data and its
license in the server image's third-party notices.
