// SegmentRow.shared.tsx — utilities, constants, and small render helpers for
// segment rows (WhitespaceText, RichSourceText, HighlightedSource,
// STATUS_CHIP_COLORS, isMac, findTextRange, serializeEditor, term-lookup
// cache, ...).
//
// External consumers (CATEditor, SegmentRow.test) import from this file.
// SegmentRow.tsx re-imports what it needs from here.

import React from 'react'
import type { TermLookupResult } from '../api'
import type { SegmentEditorHandle } from './SegmentEditor'
import { flattenInlineNodes, hasXliffTags, richTextToTipTapContent } from '../lib/xliff-tiptap'
import { chipLabel, chipTitle, labelModeFor, toLabelAttrs, type RefResolver } from '../lib/xliff-tag-label'
import { highlightText, HIGHLIGHT_CLASS } from '../lib/text-highlight'
import { termUnderlineClass } from '../lib/term-ranges'
import { qeChipLabel } from '../lib/qe-chip'
import { renderRanges, decorationRanges, hasDecorations } from '../lib/segment-decorations'
import type { ChipColor } from '@/components/ui/chip'
import { escapeRegExp } from '../lib/regex'

// termUnderlineClass lives in lib/term-ranges.ts with the rest of the matcher;
// re-exported for consumers that import it from here.
export { termUnderlineClass }

/** Render text with visible whitespace markers for source/target display. */
// Whitespace/invisible character markers for the source column (read-only).
// Each marker preserves the original character in the DOM for correct layout,
// and overlays a visible symbol via CSS ::after (ws-dot, ws-nbsp, ws-zw classes).
export const WS_MARKERS: Record<string, { cls: string; keep?: string }> = {
  ' ':      { cls: 'ws-dot', keep: ' ' },          // regular space — keep for word-wrap
  '\t':     { cls: 'ws-tab', keep: '\t' },          // tab
  ' ': { cls: 'ws-nbsp', keep: ' ' },     // non-breaking space
  '​': { cls: 'ws-zw' },                        // zero-width space
  '‌': { cls: 'ws-zw' },                        // zero-width non-joiner
  '‍': { cls: 'ws-zw' },                        // zero-width joiner
  ' ': { cls: 'ws-dot', keep: ' ' },       // en space
  ' ': { cls: 'ws-dot', keep: ' ' },       // em space
  ' ': { cls: 'ws-dot', keep: ' ' },       // thin space
  ' ': { cls: 'ws-dot', keep: ' ' },       // hair space
  '　': { cls: 'ws-dot', keep: '　' },       // ideographic space
  '­': { cls: 'ws-shy' },                        // soft hyphen (¬ traditional symbol)
  '﻿': { cls: 'ws-zw' },                        // BOM / zero-width no-break space
  ' ': { cls: 'ws-dot', keep: ' ' },       // medium mathematical space
  ' ': { cls: 'ws-dot', keep: ' ' },       // figure space
  ' ': { cls: 'ws-dot', keep: ' ' },       // three-per-em space
  ' ': { cls: 'ws-dot', keep: ' ' },       // four-per-em space
  ' ': { cls: 'ws-dot', keep: ' ' },       // six-per-em space
  ' ': { cls: 'ws-dot', keep: ' ' },       // punctuation space
  ' ': { cls: 'ws-zw' },                         // line separator
  ' ': { cls: 'ws-zw' },                         // paragraph separator
  '᠎': { cls: 'ws-zw' },                         // Mongolian vowel separator
  '': { cls: 'ws-zw' },                         // form feed
}

export function WhitespaceText({ text, highlightTerm, terms, target, diffAgainst, changesFrom, search }: {
  text: string
  highlightTerm?: string | null
  /** CAT-side term underlining. Takes precedence over
   *  highlightTerm, matching SegmentText's documented resolution order. */
  terms?: TermLookupResult[]
  target?: string
  /** Text to diff against for the TM fuzzy-match highlight. */
  diffAgainst?: string | null
  /** Earlier version of the text, for the history track changes. */
  changesFrom?: string | null
  /** Search hits to tint (Decorations.search). */
  search?: RegExp | null
}) {
  // Term underlines and the diff highlights compose with whitespace markers
  // the same way highlightTerm does below: decoration spans wrap
  // whitespace-marker leaves.
  const ranges = decorationRanges(text, { terms, target, diffAgainst, changesFrom, search })
  if (ranges.length > 0) {
    return (
      <span className="whitespace-pre-wrap">
        {renderRanges(text, ranges, 0, 'w', s => renderWhitespace(s, 0))}
      </span>
    )
  }
  // When highlightTerm is set, we need to apply both whitespace markers AND yellow highlights.
  // Strategy: first split text by highlight matches, then apply whitespace markers within each part.
  if (highlightTerm?.trim()) {
    const escaped = escapeRegExp(highlightTerm)
    // split with capturing group: odd indices are matches, even are non-matches
    const segments = text.split(new RegExp(`(${escaped})`, 'gi'))
    const result: React.ReactNode[] = []
    let keyIdx = 0
    for (let si = 0; si < segments.length; si++) {
      const seg = segments[si]
      if (si % 2 === 1) {
        // Matched text: apply whitespace markers INSIDE a <mark> wrapper
        result.push(
          <mark key={`m${keyIdx}`} className={HIGHLIGHT_CLASS}>
            {renderWhitespace(seg, keyIdx * 1000)}
          </mark>
        )
        keyIdx++
      } else if (seg) {
        result.push(...renderWhitespace(seg, keyIdx * 1000))
        keyIdx++
      }
    }
    return <span className="whitespace-pre-wrap">{result}</span>
  }

  return <span className="whitespace-pre-wrap">{renderWhitespace(text, 0)}</span>
}

/** Render whitespace markers for a text chunk. */
function renderWhitespace(text: string, keyOffset: number): React.ReactNode[] {
  const parts: React.ReactNode[] = []
  let i = keyOffset
  for (const ch of text) {
    if (ch === '\n') {
      parts.push(<span key={i} className="opacity-80 dark:opacity-90">¶</span>)
      parts.push(<br key={`br${i}`} />)
    } else if (ch in WS_MARKERS) {
      const m = WS_MARKERS[ch]
      parts.push(<span key={i} className={m.cls}>{m.keep ?? ''}</span>)
    } else {
      parts.push(ch)
    }
    i++
  }
  return parts
}

/** Find the first occurrence of `term` in `text` (case-insensitive). Returns {from, to} offsets or null. */
export function findTextRange(text: string, term: string | null | undefined): { from: number; to: number } | null {
  if (!term || !term.trim()) return null
  const idx = text.toLowerCase().indexOf(term.toLowerCase())
  if (idx === -1) return null
  return { from: idx, to: idx + term.length }
}

// Inactive segment and TM rows stop at 10 lines and end the last one with "…".
// The active row is uncapped, so clicking it reveals the rest. Tailwind's
// line-clamp is a -webkit-box, so short text keeps its natural height. The clamp
// must sit on an element without vertical padding: its overflow:hidden clips it.
export const SEGMENT_PREVIEW_CLAMP = 'line-clamp-10'

// Keyed by the DERIVED display status (deriveDisplayStatus) — not
// the raw DB status. untranslated / pre_translated are derived sub-states of
// needs_review; in-flight is tracked separately via in_flight_phase.
export const STATUS_CHIP_COLORS: Record<string, ChipColor> = {
  untranslated: 'grey',
  pre_translated: 'yellow',
  auto_approved: 'green',
  needs_review: 'red',
  approved: 'blue',
}

// Display labels for the derived UI status (deriveDisplayStatus).
// All five buckets are explicitly listed so casing/wording can't drift between
// rendering sites. Title-case + "Pretranslated" (one word) + "Auto-approved"
// (hyphenated) match the filter bar's translator-facing vocabulary. DB enum value + API + back-
// end tests still reference the raw `needs_review` etc. — this is UI-text only.
export const STATUS_LABELS: Record<string, string> = {
  untranslated:   'Untranslated',
  pre_translated: 'Pretranslated',
  needs_review:   'Draft',
  auto_approved:  'Auto-approved',
  approved:       'Approved',
}

/** Render-ready label for a (derived) segment status. */
export function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status.replace('_', ' ')
}

// Chip text per translation_history.kind, for the History section.
export const HISTORY_KIND_LABELS: Record<string, string> = {
  tm:          'TM',
  mt:          'MT',
  ape:         'APE',
  human:       'Edit',
  propagation: 'Propagated',
  file_import: 'File',
  cleared:     'Cleared',
  // Status events — no text, so the row renders compact. Every row in the panel
  // names something that happened, so the `unconfirmed` kind reads "Unapproved":
  // nobody unconfirms a segment, they edit it and the approval falls off. Same
  // passive voice as Approved. The enum value stays as it is, like
  // needs_review showing as "Draft" above.
  approved:      'Approved',
  auto_approved: 'Auto-approved',
  unconfirmed:   'Unapproved',
}

// Colour follows the same rule as the Match column: provenance is grey, and
// colour is spent only where it says something — the TM percentage, the status
// events (which reuse STATUS_CHIP_COLORS above, so "Approved" is the same
// colour in the panel as in the Status column), and `cleared`, red because
// losing a translation is not provenance.
export const HISTORY_KIND_CHIP_COLORS: Record<string, ChipColor> = {
  tm:          'green',
  mt:          'grey',
  ape:         'grey',
  human:       'grey',
  propagation: 'grey',
  file_import: 'grey',
  cleared:     'red',
  approved:      STATUS_CHIP_COLORS.approved,
  auto_approved: STATUS_CHIP_COLORS.auto_approved,
  unconfirmed:   STATUS_CHIP_COLORS.needs_review,
}

/** Yellow replaces green on a TM event below 100% — same rule as the Match chip. */
export function historyKindColor(kind: string, tmMatchPercent: number | null): ChipColor {
  if (kind === 'tm' && tmMatchPercent != null && tmMatchPercent < 100) return 'yellow'
  return HISTORY_KIND_CHIP_COLORS[kind] ?? 'grey'
}

export const OUTCOME_LABELS: Record<string, string> = {
  approved: 'QP approved',
  ape_approved: 'APE approved',
  ape_needs_review: 'APE (review)',
  rejected: 'Rejected',
}

/**
 * The QE chip's render state, or null when the segment has no score. The
 * server keeps qe_note only while it describes the segment's text: a person's
 * new text, or a rollback past the step that scored it, clears it.
 */
export function qeChipInfo(t?: {
  qe_note?: string | null
  predict_outcome?: { predict_outcome: string; valid: boolean }
}): { label: string; title: string; color: ChipColor } | null {
  const note = t?.qe_note
  const label = qeChipLabel(note)
  if (label == null || note == null) return null
  const outcome = t?.predict_outcome?.valid ? OUTCOME_LABELS[t.predict_outcome.predict_outcome] : undefined
  return { label, title: `AI quality check${outcome ? ` — ${outcome}` : ''}: ${note}`, color: 'blue' }
}

export { isMac, modKey } from '@/lib/platform'

/** Strip outer <p>...</p> wrapper from TipTap HTML output. */
function stripOuterP(html: string): string {
  return html.replace(/^<p>(.*)<\/p>$/s, '$1')
}

/** Check if text contains HTML formatting tags (TipTap output). Used by both
 *  the row component (to decide whether to render via SafeHTML) and the
 *  serializer below. */
export function hasHTMLFormatting(text: string): boolean {
  return /<(strong|em|sup|sub)>/.test(text)
}

// Simple cache for term lookups to avoid duplicate API calls for the same source text.
// Module-scoped — survives component remounts within the same page session.
export const termLookupCache = new Map<string, TermLookupResult[]>()

/** Serialize the TipTap editor content for saving. Returns plain/HTML text + XLIFF rich text.
 *  A plain-text file's text is always editText: its strings carry no HTML. */
export function serializeEditor(
  editor: SegmentEditorHandle | null,
  editText: string,
  plainText = false,
): { text: string; richText: string | undefined } {
  const html = editor?.getHTML() ?? ''
  const text = !plainText && hasHTMLFormatting(html) ? stripOuterP(html) : editText
  const richText = editor?.getRichText() ?? undefined
  return { text, richText }
}

/** Render source text with XLIFF inline tags as styled chips.
 *  Reuses the token-based parser from xliff-tiptap.ts to handle nested <g> tags.
 *  When `terms` is provided, term underlines are painted over the text nodes
 *  BETWEEN the chips: matching runs over the flattened plain text (which is
 *  what the user actually reads — chips contribute nothing, hard breaks
 *  contribute a newline), then each text node paints the ranges that intersect
 *  its window. A term straddling a chip underlines both halves as ONE
 *  occurrence. This is why decoration is range-based. */
export function RichSourceText({ richText, showWhitespace, resolveRef, terms, target, diffAgainst, changesFrom, search }: {
  richText: string
  showWhitespace?: boolean
  /** Resolves a cross-reference chip to the term it renders. */
  resolveRef?: RefResolver
  /** CAT-side term underlining across the segment's text nodes. */
  terms?: TermLookupResult[]
  target?: string
  /** Text to diff against for the TM fuzzy-match highlight. Diffed against the
   *  FLATTENED text, so the comparison is what the reader sees, not the markup. */
  diffAgainst?: string | null
  /** Earlier version for the history track changes, compared with the
   *  flattened text like `diffAgainst`. */
  changesFrom?: string | null
  /** Search hits to tint, matched against the flattened text. */
  search?: RegExp | null
}) {
  const doc = richTextToTipTapContent(richText)
  const nodes = doc.content?.[0]?.content || []
  // Flatten to the plain text the user reads, remembering where each node
  // starts so per-node rendering can window the shared ranges.
  const { flat, offsets: nodeOffsets } = flattenInlineNodes(nodes)
  const ranges = decorationRanges(flat, { terms, target, diffAgainst, changesFrom, search })
  // Each text node is one render window; a removed-text ghost belongs to the
  // last window starting at or before it (see RangeWindow).
  const textNodes = nodes.flatMap((node, i) => (node.type === 'text' ? [i] : []))
  return (
    <span className={showWhitespace ? 'whitespace-pre-wrap' : undefined}>
      {nodes.map((node, i) => {
        if (node.type === 'text') {
          const nodeText = node.text ?? ''
          if (ranges.length > 0) {
            const leaf = showWhitespace ? (s: string) => renderWhitespace(s, 0) : (s: string) => s
            const t = textNodes.indexOf(i)
            const next = textNodes[t + 1]
            const textWindow = { nextOffset: next === undefined ? Infinity : nodeOffsets[next], first: t === 0 }
            return <span key={i}>{renderRanges(nodeText, ranges, nodeOffsets[i], `n${i}`, leaf, textWindow)}</span>
          }
          if (showWhitespace) return <WhitespaceText key={i} text={nodeText} />
          return <span key={i}>{node.text}</span>
        }
        if (node.type === 'hardBreak') {
          // Line breaks (from imported <br> or saved
          // <x ctype="lb"/> markers) parse to hardBreak nodes. Render a
          // visible ¶ + <br/> when the whitespace toggle is on, just a
          // <br/> otherwise — matches the editor-side behavior and the
          // read-only WhitespaceText component's \n handling.
          if (showWhitespace) {
            return <span key={i}><span className="ws-pilcrow" aria-hidden="true">¶</span><br /></span>
          }
          return <br key={i} />
        }
        if (node.type === 'xliffTag') {
          // Labels are derived here rather than stored on the node, and the
          // `¶` toggle doubles as "show full tag names".
          // Hover always reveals the element form.
          const attrs = toLabelAttrs(node.attrs)
          // Cross-reference chips are deliberately NOT click-to-jump: a chip
          // sits inside a row whose click activates the segment, and a jump on
          // plain click teleported linguists away when they merely meant to
          // activate the row. The jump lives in the sidebar's References
          // section instead.
          return (
            <span
              key={i}
              className="xliff-tag"
              data-tag-type={attrs.tagType}
              data-tag-id={attrs.tagId}
              data-ctype={attrs.ctype}
              title={chipTitle(attrs)}
            >
              {chipLabel(attrs, labelModeFor(showWhitespace), resolveRef)}
            </span>
          )
        }
        return null
      })}
    </span>
  )
}

// The matchers themselves (per-occurrence term coloring in lib/term-ranges.ts,
// the fuzzy diff in lib/tm-diff.ts) live elsewhere — this component is just the
// plain-text painter over their ranges.
export function HighlightedSource({ text, terms, target, diffAgainst, changesFrom, search }: {
  text: string
  terms?: TermLookupResult[]
  target?: string
  diffAgainst?: string | null
  changesFrom?: string | null
  search?: RegExp | null
}) {
  const ranges = decorationRanges(text, { terms, target, diffAgainst, changesFrom, search })
  if (!ranges.length) return <>{text}</>
  return <>{renderRanges(text, ranges, 0, 'h', s => s)}</>
}

// `hasXliffTags` is re-exported here so SegmentRow.tsx can keep a single
// import surface for shared helpers.
export { hasXliffTags }

// ─── <SegmentText> ─────────────────────────────────────────────────────────
//
// Single text-rendering component shared by every read-only surface that
// shows segment-shaped text: TM browser cells, Concordance modal, and any
// other "view a segment without editing it" UI.
//
// One tag-rendering layer for every read-only pane: the editor itself is
// TipTap (separate by necessity), but every read-only pane goes through this
// single component so tags render the same everywhere.
//
// Resolution order (first matching wins):
//   1. richText present + has XLIFF tags → render via the parser → atomic chips
//   2. showWhitespace → render with whitespace markers (¶, dot, …)
//   3. terms, diffAgainst, changesFrom and/or search present → range
//      decorations (term underlines, TM fuzzy diff, history track changes,
//      search hits)
//   4. highlightTerm (Find / Concordance search hit) → wrap in yellow <mark>
//   5. plain text
//
// All five are layered so callers pick what they need:
//
//   <SegmentText text={r.source_text} richText={r.source_rich_text} />
//   <SegmentText text={r.target_text} richText={r.target_rich_text}
//                highlightTerm={searchQuery} />
//   <SegmentText text={seg.source_text} richText={seg.source_rich_text}
//                terms={matchingTerms} showWhitespace={showWhitespace} />
//
// Range decorations (lib/segment-decorations.tsx) ride every path: they are
// offset ranges over the flattened plain text, painted per text node, so chips
// and whitespace markers keep them.
//
// `highlightTerm` is the exception: its offsets are computed
// against `text`, which is NOT what the chip renderer displays (placeholders
// are stripped from the flattened text), so it is dropped on paths 1–2's rich
// branch. Chips win over it. `search` is the range-based search highlight and
// survives chips.
export interface SegmentTextProps {
  text: string
  richText?: string | null
  highlightTerm?: string | null
  terms?: TermLookupResult[]
  /** Current target text — enables green/red term verdicts (editor path).
   *  Omit in read-only panes for neutral underlines. */
  target?: string
  /** Comparison text for the TM fuzzy-match highlight: the parts of `text`
   *  that this string does NOT have are painted yellow (lib/tm-diff.ts). */
  diffAgainst?: string | null
  /** Earlier version of `text`, for the history track changes: added words
   *  are painted yellow and removed words drawn struck through
   *  (lib/track-changes.ts). Empty means nothing to compare with. */
  changesFrom?: string | null
  /** Search hits to tint quietly (lib/text-search.ts `searchHighlightRegex`).
   *  Matched against the text the reader sees, so it works across chips. */
  search?: RegExp | null
  showWhitespace?: boolean
  /** Resolves a cross-reference chip to the term it renders. */
  resolveRef?: RefResolver
}

export function SegmentText({ text, richText, highlightTerm, terms, target, diffAgainst, changesFrom, search, showWhitespace, resolveRef }: SegmentTextProps) {
  // Path 1: rich text with actual tags wins — chips render, and term
  // underlines, the fuzzy diff and `search` compose; `highlightTerm` is dropped
  // here (see note above).
  // Chip detection is structural — only routes through
  // the chip parser when `richText` is populated. Plain text bodies are
  // never auto-promoted to chips. The
  // Go-side parsers populate `richText` at import time when they detect
  // HTML inline tags.
  if (richText && hasXliffTags(richText)) {
    return <RichSourceText richText={richText} showWhitespace={showWhitespace} resolveRef={resolveRef} terms={terms} target={target} diffAgainst={diffAgainst} changesFrom={changesFrom} search={search} />
  }
  // Path 2: whitespace markers. Term underlines take precedence over the
  // highlightTerm yellow (same order as paths 3 vs 4); both compose with the
  // markers via WhitespaceText.
  if (showWhitespace) {
    return <WhitespaceText text={text} highlightTerm={highlightTerm} terms={terms} target={target} diffAgainst={diffAgainst} changesFrom={changesFrom} search={search} />
  }
  // Path 3: range decorations — approved-term underlining, the TM fuzzy
  // diff, the history track changes and/or search hits.
  if (hasDecorations({ terms, diffAgainst, changesFrom, search })) {
    return <HighlightedSource text={text} terms={terms} target={target} diffAgainst={diffAgainst} changesFrom={changesFrom} search={search} />
  }
  // Path 4: search highlight (Find, Concordance).
  if (highlightTerm) {
    return <>{highlightText(text, highlightTerm)}</>
  }
  // Path 5: plain text.
  return <>{text}</>
}
