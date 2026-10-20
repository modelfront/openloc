import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { SegmentText, HighlightedSource } from './SegmentRow.shared'
import type { TermLookupResult } from '../api'
import { TM_DIFF_TINT } from '../lib/tm-diff'
import { SEARCH_HIT_TINT } from '../lib/text-highlight'
import { markTexts } from '../test/text-marks'

// SegmentText is the single read-only text renderer used by TM Browser,
// Concordance modal, and any future segment-shaped read-only surface.
// These tests pin the resolution order in its docstring (rich → whitespace
// → term highlight → find highlight → plain) so a future refactor can't
// silently change the priority and break a downstream surface.
//
// Paths 1 and 4 are the ones the TM browser + Concordance
// rely on (rich text for tag chips, find-highlight for search yellow). The
// other paths are exercised by the editor and pinned here too, so every
// surface can share this one component.

describe('SegmentText (read-only renderer)', () => {
  it('plain text path: renders text as-is when no rich/highlight/terms/whitespace', () => {
    const { container } = render(<SegmentText text="Hello world" />)
    expect(container.textContent).toBe('Hello world')
    // Anti-tautology — verify no decoration leaked through:
    expect(container.querySelector('.xliff-tag')).toBeNull()
    expect(container.querySelector('mark')).toBeNull()
  })

  it('rich text path: renders XLIFF tags as atomic .xliff-tag chips', () => {
    // <bpt id="1">[bold]</bpt>here<ept id="1">[/bold]</ept>
    // The displaytext attribute is what shows inside the chip; the tag itself
    // is a non-editable atomic node.
    const rich = `Click <bpt id="1" ctype="bold" displaytext="[B]"/>here<ept id="1" displaytext="[/B]"/>`
    const { container } = render(<SegmentText text="Click here" richText={rich} />)
    const chips = container.querySelectorAll('.xliff-tag')
    // Two chips for paired tags
    expect(chips.length).toBeGreaterThanOrEqual(1)
    // The "here" text between the chips must still be visible
    expect(container.textContent).toContain('here')
  })

  it('rich text path: null/empty rich falls through to plain text', () => {
    const { container: nullCase } = render(<SegmentText text="Plain" richText={null} />)
    expect(nullCase.textContent).toBe('Plain')
    expect(nullCase.querySelector('.xliff-tag')).toBeNull()

    const { container: emptyCase } = render(<SegmentText text="Plain" richText="" />)
    expect(emptyCase.textContent).toBe('Plain')
  })

  it('rich text path: rich text WITHOUT XLIFF tags falls through (only chip-able tags trigger chip path)', () => {
    // hasXliffTags is the parser's "is this worth running" check — if the
    // string has no <bpt|ept|ph|...> tag, we skip parsing and render plain.
    // Important: a TM entry's rich_text might be set but contain only HTML
    // formatting (<strong>, <em>) — that's not the chip path.
    const { container } = render(<SegmentText text="Plain" richText="just plain text" />)
    expect(container.textContent).toBe('Plain')
    expect(container.querySelector('.xliff-tag')).toBeNull()
  })

  it('find highlight path: wraps the matched term in <mark> when no richText', () => {
    const { container } = render(<SegmentText text="Find the password here" highlightTerm="password" />)
    const marks = container.querySelectorAll('mark')
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('password')
  })

  it('priority: rich text wins over find highlight (chips don\'t mix with mid-text decorations)', () => {
    // Per the docstring: once tags are atomic chips, mid-text yellow marks
    // can't render across tag boundaries. The rich path takes precedence and
    // the find highlight is silently dropped.
    const rich = `<ph id="1" displaytext="[X]"/>some text`
    const { container } = render(
      <SegmentText text="some text" richText={rich} highlightTerm="text" />
    )
    expect(container.querySelector('.xliff-tag')).not.toBeNull()
    expect(container.querySelector('mark')).toBeNull() // highlight skipped
  })

  it('whitespace path: renders ¶/dot/zw markers when showWhitespace', () => {
    const { container } = render(<SegmentText text="a b" showWhitespace />)
    // WhitespaceText puts each whitespace char in a span with a ws-* class.
    // For a regular space we expect a span.ws-dot.
    expect(container.querySelector('.ws-dot')).not.toBeNull()
  })

  it('term highlight path: underlines matched terms when terms array supplied', () => {
    // Cast to satisfy the full TermLookupResult shape — only `source_term`,
    // `target_term`, `status` are read by HighlightedSource for the underline
    // class + tooltip.
    const terms = [
      { source_term: 'password', target_term: 'contraseña', status: 'approved' } as TermLookupResult,
    ]
    const { container } = render(<SegmentText text="Enter your password" terms={terms} />)
    // HighlightedSource wraps matches in span.underline
    expect(container.querySelector('span.underline')).not.toBeNull()
  })
})

// The live source underline colors EACH occurrence
// independently. With 2× source and only 1× correct target, the first source
// occurrence is covered (green) and the second is uncovered (red) — not both
// red. Reported by QA: "only one term translation is wrong but both are red".
describe('HighlightedSource per-occurrence coloring', () => {
  const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
    id: 1, concept_id: 1, source_term: 'password', target_term: 'Passwort', mt_translation: '',
    status: 'approved', notes: '', domain: '', term_base_id: 1,
    term_base_name: 'tb', priority: 0, ...over,
  })

  it('colors the first occurrence green and the extra one red', () => {
    // Target has one correct "Passwort" and one wrong "Passwortt" (not a whole
    // "Passwort" — trailing t). So covered = 1: occurrence 1 green, 2 red.
    const { container } = render(
      <HighlightedSource
        text="Enter your new password, repeat your password to verify."
        terms={[term()]}
        target="Geben Sie Ihr neues Passwort ein, wiederholen Sie Ihr Passwortt."
      />,
    )
    const greens = container.querySelectorAll('span.decoration-green-500')
    const reds = container.querySelectorAll('span.decoration-red-500')
    expect(greens.length).toBe(1)
    expect(reds.length).toBe(1)
  })

  it('colors both green when both occurrences are correctly translated', () => {
    const { container } = render(
      <HighlightedSource
        text="Enter your password, repeat your password."
        terms={[term()]}
        target="Passwort eingeben, Passwort wiederholen."
      />,
    )
    expect(container.querySelectorAll('span.decoration-green-500').length).toBe(2)
    expect(container.querySelectorAll('span.decoration-red-500').length).toBe(0)
  })

  // Concept redesign: approved target synonyms are ALTERNATIVES. Using ONE of
  // them must keep the source green — the unused synonym must NOT paint it red.
  // (Both rows share concept_id via the factory default.)
  it('treats approved synonyms of one concept as alternatives', () => {
    const { container } = render(
      <HighlightedSource
        text="Enter your password."
        terms={[term(), term({ id: 2, target_term: 'Kennwort' })]}
        target="Passwort eingeben."
      />,
    )
    expect(container.querySelectorAll('span.decoration-green-500').length).toBe(1)
    expect(container.querySelectorAll('span.decoration-red-500').length).toBe(0)
  })
})

// Pins that term underlines survive the chip path: decoration is a set of
// ranges over the FLATTENED rich text, which every renderer can paint. The
// plain-text renderer is no fallback — placeholders are stripped from
// `source_text`, so its offsets would be wrong.
describe('term highlighting composes with tag chips', () => {
  const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
    id: 1, concept_id: 1, source_term: 'humidity', target_term: 'Feuchtigkeit', mt_translation: '',
    status: 'approved', notes: '', domain: '', term_base_id: 1,
    term_base_name: 'tb', priority: 0, ...over,
  })

  // Verbatim from the reported row (project 18 / job 116, position 119): a <g>
  // subscript pair around the "2" of CO2 plus two ST4 cross-reference
  // placeholders. Its plain `source_text` is the flattened form below — note
  // the placeholders leave a double space behind.
  const RICH = 'For CO<g id="1" ctype="x-html-sub">2</g>, the humidity correction can be set to <x id="2" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref&gt;" ref_key="/tms/tmsnode[14]/metalist/meta"/> or <x id="3" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref&gt;" ref_key="/tms/tmsnode[15]/metalist/meta"/>.'
  const PLAIN = 'For CO2, the humidity correction can be set to  or .'

  it('underlines the term while still rendering the chips', () => {
    const { container } = render(
      <SegmentText text={PLAIN} richText={RICH} terms={[term()]} target="Für CO2 kann die Feuchtigkeitskorrektur eingestellt werden." />,
    )
    // Chips must survive — this is the whole reason the path exists.
    expect(container.querySelectorAll('span.xliff-tag').length).toBe(4)
    const marks = container.querySelectorAll('span.underline')
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('humidity')
    // "Feuchtigkeit" is not a whole word in "Feuchtigkeitskorrektur", so the
    // term is uncovered → red, same verdict the QA panel gives.
    expect(container.querySelectorAll('span.decoration-red-500').length).toBe(1)
    // Text must not be duplicated or dropped by the decoration pass.
    expect(container.textContent).toContain('the humidity correction can be set to')
  })

  it('greens the term when the target actually contains the translation', () => {
    const { container } = render(
      <SegmentText text={PLAIN} richText={RICH} terms={[term()]} target="Die Feuchtigkeit kann eingestellt werden." />,
    )
    expect(container.querySelectorAll('span.decoration-green-500').length).toBe(1)
  })

  it('does not underline (or claim green/red for) a sub-word hit split by a chip', () => {
    // Flattened text is "CO2", so the term "CO" is only ever a sub-word hit
    // ("CO2", and "co" inside "correction"). The whole-word gate drops sub-word
    // hits entirely — same as the plain-text path — so there
    // are no underlines at all, and certainly no green/red verdict.
    const { container } = render(
      <SegmentText text={PLAIN} richText={RICH} terms={[term({ source_term: 'CO', target_term: 'CO' })]} target="CO ist da." />,
    )
    expect(container.querySelectorAll('span.underline').length).toBe(0)
    expect(container.querySelectorAll('span.decoration-green-500').length).toBe(0)
    expect(container.querySelectorAll('span.decoration-red-500').length).toBe(0)
  })

  it('underlines both halves of a term split by a chip, as one occurrence', () => {
    const rich = 'Enter your email<g id="1" ctype="x-html-b"> address</g> here'
    const { container } = render(
      <SegmentText
        text="Enter your email address here"
        richText={rich}
        terms={[term({ source_term: 'email address', target_term: 'E-Mail-Adresse' })]}
        target="E-Mail-Adresse hier eingeben"
      />,
    )
    const marks = Array.from(container.querySelectorAll('span.underline'))
    expect(marks.map(m => m.textContent)).toEqual(['email', ' address'])
    // One occurrence, one verdict — both halves share the same color.
    expect(container.querySelectorAll('span.decoration-green-500').length).toBe(2)
  })

  it('keeps underlines when the whitespace toggle is on (rich path)', () => {
    const { container } = render(
      <SegmentText text={PLAIN} richText={RICH} terms={[term()]} target="" showWhitespace />,
    )
    expect(container.querySelectorAll('span.xliff-tag').length).toBe(4)
    expect(container.querySelectorAll('span.underline').length).toBe(1)
    expect(container.querySelector('.ws-dot')).not.toBeNull()
  })

  it('keeps underlines when the whitespace toggle is on (plain path)', () => {
    const { container } = render(
      <SegmentText text="Enter your humidity value" terms={[term()]} target="" showWhitespace />,
    )
    const marks = container.querySelectorAll('span.underline')
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('humidity')
    expect(container.querySelector('.ws-dot')).not.toBeNull()
  })
})

// Cross-reference chips are deliberately NOT click-to-jump — a
// chip sits inside a row whose click activates the segment, and a jump on
// plain click teleported linguists away when they merely meant to activate the
// row. The jump lives in the sidebar's References section (EditorSidebar).
// This pins the chip staying inert so the affordance doesn't sneak back.
describe('cross-reference chips carry no click affordance', () => {
  it('renders a cross-ref chip like any other chip — no pointer, no click hint', () => {
    const rich = 'See <x id="1" ctype="x-xml-meta-ref" original_markup="&lt;meta-ref&gt;" ref_key="/tms/tmsnode[14]/metalist/meta"/> for details'
    const { container } = render(<SegmentText text="See  for details" richText={rich} />)
    const chip = container.querySelector('span.xliff-tag')!
    expect(chip.className).not.toContain('cursor-pointer')
    expect(chip.getAttribute('title') ?? '').not.toContain('click to open')
  })
})

// TM fuzzy-diff highlight (lib/tm-diff.ts). `diffAgainst` paints the parts of
// `text` the comparison string does NOT have. Both the TM card's source line
// and the active segment's source cell use it, each pointed at the other, so
// the two directions of the difference are visible side by side.
describe('TM fuzzy-diff highlight', () => {
  // Keyed on the tint alone, so a padding or radius tweak doesn't break these.
  const HL = `span.${TM_DIFF_TINT}`

  it('highlights only what the text adds over the comparison string', () => {
    const { container } = render(
      <SegmentText text="Reset your admin password." diffAgainst="Reset your password." />,
    )
    const marks = container.querySelectorAll(HL)
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('admin ')
    // The rest of the sentence must survive intact — a painter that dropped
    // the undecorated gaps would still pass the assertion above.
    expect(container.textContent).toBe('Reset your admin password.')
  })

  it('highlights nothing when the two strings match', () => {
    const { container } = render(
      <SegmentText text="Reset your password." diffAgainst="Reset your password." />,
    )
    expect(container.querySelector(HL)).toBeNull()
    expect(container.textContent).toBe('Reset your password.')
  })

  it('highlights nothing on the side that only drops words', () => {
    const { container } = render(
      <SegmentText text="Reset your password." diffAgainst="Reset your admin password." />,
    )
    expect(container.querySelector(HL)).toBeNull()
  })

  it('renders chips AND the highlight on a tagged hit', () => {
    const rich = 'Click <bpt id="1" ctype="bold" displaytext="[B]"/>Save now<ept id="1" displaytext="[/B]"/>'
    const { container } = render(
      <SegmentText text="Click Save now" richText={rich} diffAgainst="Click Save" />,
    )
    expect(container.querySelectorAll('span.xliff-tag').length).toBe(2)
    const marks = container.querySelectorAll(HL)
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe(' now')
  })

  it('composes with a term underline on the same word', () => {
    const terms = [
      { id: 1, concept_id: 1, source_term: 'password', target_term: 'Passwort', mt_translation: '',
        status: 'approved', notes: '', domain: '', term_base_id: 1, term_base_name: 'tb',
        priority: 0 } as TermLookupResult,
    ]
    const { container } = render(
      <SegmentText text="Enter the password" terms={terms} target="Passwort" diffAgainst="Enter the" />,
    )
    // One span carries both decorations rather than one winning over the other.
    const both = container.querySelector(`span.underline.${TM_DIFF_TINT}`)
    expect(both).not.toBeNull()
    expect(both!.textContent).toBe('password')
  })

  it('keeps the highlight when the whitespace toggle is on', () => {
    const { container } = render(
      <SegmentText text="Reset your admin password." diffAgainst="Reset your password." showWhitespace />,
    )
    const marks = container.querySelectorAll(HL)
    expect(marks.length).toBe(1)
    expect(marks[0].textContent).toBe('admin ')
    expect(container.querySelector('.ws-dot')).not.toBeNull()
  })
})

// History track changes (lib/track-changes.ts). `changesFrom` is the earlier
// version: words it lost are drawn inline, struck through, before the words
// that replaced them, which are highlighted.
describe('track-changes highlight', () => {
  const HL = `span.${TM_DIFF_TINT}`
  const DEL = 'span.line-through'

  it('draws the deleted word struck through in red, then the new word highlighted', () => {
    const { container } = render(<SegmentText text="Denuncia recibida" changesFrom="Denuncia enviada" />)
    expect(markTexts(container, DEL)).toEqual(['enviada'])
    expect(container.querySelector(DEL)!.className).toContain('bg-red-100')
    expect(markTexts(container, HL)).toEqual(['recibida'])
    // Reading order: the old word sits right before its replacement, and the
    // unchanged text survives around them.
    expect(container.textContent).toBe('Denuncia enviadarecibida')
  })

  it('marks nothing when there is no earlier version', () => {
    const { container } = render(<SegmentText text="Denuncia enviada" changesFrom="" />)
    expect(container.querySelector(DEL)).toBeNull()
    expect(container.querySelector(HL)).toBeNull()
    expect(container.textContent).toBe('Denuncia enviada')
  })

  it('draws a deletion at the very end once', () => {
    const { container } = render(<SegmentText text="Hola" changesFrom="Hola mundo" />)
    expect(markTexts(container, DEL)).toEqual(['mundo'])
    expect(container.textContent).toBe('Hola mundo')
  })

  it('keeps the deleted text visible when the whitespace toggle is on', () => {
    const { container } = render(<SegmentText text="Reset your password" changesFrom="Reset your admin password" showWhitespace />)
    expect(markTexts(container, DEL)).toEqual(['admin'])
  })

  // Text between tag chips is drawn node by node. A deletion sitting on a
  // node boundary must land in exactly one node, or it is drawn twice — or
  // not at all.
  it('draws each deletion once on tagged text, next to chips and line breaks', () => {
    const rich = 'Click <bpt id="1" ctype="bold" displaytext="[B]"/>Save<ept id="1" displaytext="[/B]"/><x id="2" ctype="lb"/>Done'
    const { container } = render(
      <SegmentText text="Click Save Done" richText={rich} changesFrom={'Click the Save now\nAll done'} />,
    )
    expect(container.querySelectorAll('span.xliff-tag').length).toBe(2)
    const deleted = markTexts(container, DEL)
    expect(deleted.sort()).toEqual(['All done', 'now', 'the'])
    // The spaces those runs removed are still drawn, just outside the tint.
    expect(container.textContent).toContain('the Save now')
    expect(markTexts(container, HL)).toEqual(['Done'])
  })
})

// The quiet search tint (TM browser search). Range-based, so unlike
// highlightTerm it survives tag chips and whitespace markers.
describe('search hit highlight', () => {
  const HIT = `span[class~="${SEARCH_HIT_TINT}"]`

  it('tints every hit and keeps the rest of the text', () => {
    const { container } = render(<SegmentText text="Save, then save again" search={/save/gi} />)
    expect(markTexts(container, HIT)).toEqual(['Save', 'save'])
    expect(container.textContent).toBe('Save, then save again')
  })

  it('tints a hit on a tagged entry and still renders the chips', () => {
    const rich = 'Click <bpt id="1" ctype="bold" displaytext="[B]"/>Save now<ept id="1" displaytext="[/B]"/>'
    const { container } = render(<SegmentText text="Click Save now" richText={rich} search={/save/gi} />)
    expect(container.querySelectorAll('span.xliff-tag').length).toBe(2)
    expect(markTexts(container, HIT)).toEqual(['Save'])
  })

  it('keeps the tint when the whitespace toggle is on', () => {
    const { container } = render(<SegmentText text="Save now" search={/save/gi} showWhitespace />)
    expect(markTexts(container, HIT)).toEqual(['Save'])
    expect(container.querySelector('.ws-dot')).not.toBeNull()
  })

  it('tints nothing when nothing matches', () => {
    const { container } = render(<SegmentText text="Cancel" search={/save/gi} />)
    expect(container.querySelector(HIT)).toBeNull()
    expect(container.textContent).toBe('Cancel')
  })
})
