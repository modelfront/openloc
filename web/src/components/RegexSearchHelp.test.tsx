import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RegexSearchHelp } from './RegexSearchHelp'
import { REGEX_HELP_PATTERNS } from '@/lib/regex-help-patterns'

// The cheat sheet is copy-paste material: every row lands in the search box
// verbatim. A row whose pattern doesn't match the text its description
// promises is worse than no row at all, so each one carries a sample it must
// find. The TM search runs the same rows in Postgres — pinned by
// entry_search_integration_test.go.
describe('regex cheat sheet', () => {
  it.each(REGEX_HELP_PATTERNS)('$pattern is a working pattern', ({ pattern, sample }) => {
    // buildTextMatcher compiles the raw string with no flags and calls .test —
    // an invalid pattern would throw here and match nothing in the UI.
    const re = new RegExp(pattern)
    expect(re.test(sample)).toBe(true)
  })

  it('has no duplicate rows', () => {
    const patterns = REGEX_HELP_PATTERNS.map(p => p.pattern)
    expect(new Set(patterns).size).toBe(patterns.length)
  })
})

describe('RegexSearchHelp popover', () => {
  afterEach(cleanup)

  it('opens as a dialog named by its title and described by its intro', async () => {
    render(<RegexSearchHelp />)
    await userEvent.click(screen.getByTestId('regex-help-trigger'))

    // base-ui wires aria-labelledby / aria-describedby only through
    // PopoverTitle / PopoverDescription — a hand-rolled <p> heading leaves the
    // dialog unnamed for assistive tech.
    const dialog = await screen.findByRole('dialog', { name: 'Regex search' })
    const describedBy = dialog.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy!)?.textContent).toMatch(/^The pattern matches anywhere/)
  })
})

describe('RegexSearchHelp TM variant', () => {
  afterEach(cleanup)

  it('names the Target scope and leaves out the empty-text row', async () => {
    render(<RegexSearchHelp variant="tm" />)
    await userEvent.click(screen.getByTestId('regex-help-trigger'))
    const dialog = await screen.findByRole('dialog', { name: 'Regex search' })
    expect(dialog.textContent).toContain('Both, Source or Target')
    expect(dialog.textContent).toContain('tick Exact')
    expect(dialog.textContent).not.toContain('finds untranslated')
    // The shared rows are still there.
    expect(dialog.textContent).toContain(String.raw`\bserver\b`)
  })
})
