# Inline tag handling

How inline tags (XLIFF `<x id="1"/>`, `<g>`, `<bpt>`/`<ept>`, `<ph>`, `<it>`, `<hi>`, and HTML `<b>`/`<i>`/`<a>` in non-HTML formats) are kept intact so a translator can move them but not change their structure.

Read from code only. No tests run and the editor was not exercised.

## Summary

- The editor keeps each tag as an atomic chip. The translator can move or delete it but can't edit inside it.
- The stored markup is kept verbatim. Saving writes each tag's original text back unchanged.
- The server checks tag identity only through QA rules. Nothing enforces structure unconditionally for XLIFF.

## 1. Import

`internal/parsers/xliff/xliff.go`

- XLIFF inline elements go into `Segment.RichText`. `Segment.Text` is the tag-free version.
- Entity-escaped HTML in `<source>`/`<target>` (`&lt;b&gt;`) goes through `common.DetectInlineTags`, which chips it the same way.
- Stored in `segments.source_rich_text` and `translations.active_rich_text`.

## 2. Editor

`web/src/lib/xliff-tiptap.ts`, `web/src/lib/tiptap-xliff-tag.tsx`

- Rich text is tokenized into `xliffTag` nodes. The node is `atom`, `draggable`, and `contenteditable=false`.
- Each node keeps its original markup in a `rawXml` attribute. Saving serializes `rawXml` back verbatim, so only the text between tags changes.
- Labels (`<1>`, `{1}`) are computed at render time from node attributes. They are not stored, and toggling the label mode doesn't change the document.
- Pair numbering uses `rid`, then `i`, then `id`. Closing tags inherit identity from their opener through a stack, so both halves of a pair show the same number.
- Typed self-closing tags (`<ph id="X"/>`, `<x id="X"/>`) and paired `<bpt>`/`<ept>` convert to chips through input rules.
- Missing tags bar (`web/src/components/MissingTagsBar.tsx`): lists source tags absent from the target. A click inserts that chip at the cursor. F8 inserts the next one; Alt+F8 inserts all.

## 3. Machine translation and TM output

- MT returns plain text. `internal/pipeline/tagtransfer.go` (`TransferTags`) projects source tags onto it using verbatim match, then word-index projection, then a character-proportional fallback.
- TM matches are re-anchored with "text from the TM, tags from the source". Server side: `internal/pipeline/tm_tagtransfer.go` (`remapTagsFromSource`). Click path: `web/src/lib/tm-tag-remap.ts`. The source's tags are used only when both sides have the same number of logical tag units.
- LLM prompts get a compact chip form (`<4>…</4>`, `{2}`). `internal/inlinetag/tags.go` decodes it back to the original markup. Its labels must match the editor's, per the package comment.

## 4. QA and confirm gate

- `internal/qa/checker.go` (`CheckTagsDetailed`) reports missing, extra, unclosed and unopened tags. Each source tag is compared against the target by its display form (`{1}`, `<1>`, `</1>`), so attributes are ignored on purpose (comment at `checker.go:445`).
- `internal/translation/service.go` (`checkQA`) blocks a confirm when any finding has error severity. It returns nil if there is no QA checker, no profile, or no error-severity finding.
- `internal/translation/source_tags.go` (`foreignTagIssues`) runs on every confirm for JSON, PO, TXT and XLSX files whose strings are HTML. It refuses any target tag the source doesn't have, whether or not the project has a QA profile.

## 5. Export

`internal/parsers/xliff/xliff_escape.go` (`xliffRichToXML`)

- Escapes text runs and writes tags back as well-formed XML. `xliffTargetXML` in `xliff.go` calls it when the text contains inline tags.
- Only the target text is read. Export doesn't compare its tags to the source's.

## Gaps

1. **Attributes aren't compared.** A target `<x id="1" ctype="foo"/>` counts as the same tag as the source's `<x id="1"/>`. The server never checks that a target tag's markup equals the source's.
2. **No unconditional server gate for XLIFF.** The source-tag check only covers HTML-string formats. For XLIFF, enforcement depends on the project's QA profile and its error-severity rules. The default severity of `tag_checks` was not verified.
3. **Typed tags are minted by the translator.** Typing `<x id="9"/>` creates a chip. Only QA `tag_extra` catches a tag the source doesn't have, and only if that rule is enabled.
4. **Export doesn't check the source.** `xliffRichToXML` guarantees valid XML, not matching tags.
5. **Ordering and nesting aren't checked.** Sibling `</g>` closers all display as `{/g}`, so QA counts them but can't tell which `<g>` each one closes.
