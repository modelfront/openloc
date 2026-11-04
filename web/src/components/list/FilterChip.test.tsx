// FilterOptions — the option list inside a filter popover. Search is on by
// default and matches an option's text, or its `keywords` when the option is
// not plain text; a short fixed list turns it off.

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { FilterOption, FilterOptions } from './FilterChip'

const noop = () => {}

const LANGUAGES = [{ code: 'FR', name: 'French' }, { code: 'DE', name: 'German' }]

// Shaped like a real filter's `render`: one fragment holding "All" and a mapped
// array, so the search has to look through both.
function renderList(searchable?: boolean) {
  render(
    <FilterOptions testId="list-search" searchable={searchable}>
      <>
        <FilterOption selected onClick={noop}>All</FilterOption>
        {/* Not plain text: matched on its keywords. */}
        {LANGUAGES.map(l => (
          <FilterOption key={l.code} selected={false} onClick={noop} keywords={`${l.code} ${l.name}`}>
            <span>{l.code}</span> — {l.name}
          </FilterOption>
        ))}
        <FilterOption selected={false} onClick={noop}>Ana Ruiz</FilterOption>
      </>
    </FilterOptions>,
  )
}

// The option rows: every button but the search box's own clear button.
const options = () => screen.queryAllByRole('button').filter(b => !b.closest('[data-slot=input]'))
const shown = () => options().map(b => b.textContent)
// "No matches" must show only when nothing matched: it is worked out apart
// from the rows hiding themselves.
const expectMatches = (rows: string[]) => {
  expect(shown()).toEqual(rows)
  expect(screen.queryByText('No matches')).not.toBeInTheDocument()
}

describe('FilterOptions', () => {
  it('shows a search box by default and narrows the options as you type', () => {
    renderList()
    const box = screen.getByTestId('list-search')
    expect(shown()).toEqual(['All', 'FR — French', 'DE — German', 'Ana Ruiz'])

    fireEvent.change(box, { target: { value: 'GERM' } })
    expectMatches(['DE — German'])

    // Matches a code too, through the keywords.
    fireEvent.change(box, { target: { value: 'fr' } })
    expectMatches(['FR — French'])

    fireEvent.change(box, { target: { value: 'ana' } })
    expectMatches(['Ana Ruiz'])
  })

  it('says so when nothing matches, and shows every option again once cleared', () => {
    renderList()
    const box = screen.getByTestId('list-search')
    fireEvent.change(box, { target: { value: 'zzz' } })
    expect(options()).toHaveLength(0)
    expect(screen.getByText('No matches')).toBeInTheDocument()

    fireEvent.change(box, { target: { value: '' } })
    expect(shown()).toHaveLength(4)
    expect(screen.queryByText('No matches')).not.toBeInTheDocument()
  })

  it('has no search box for a short fixed list', () => {
    renderList(false)
    expect(screen.queryByTestId('list-search')).not.toBeInTheDocument()
    expect(shown()).toHaveLength(4)
  })
})
