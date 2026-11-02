import { useState } from 'react'
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AssetFilterBar } from './AssetFilterBar'

const ANA = { id: 1, name: 'Ana' }
const BEN = { id: 2, name: 'Ben' }

function renderBar({ total = 2, search = '', creator = null as number | null, options = [ANA, BEN] } = {}) {
  render(
    <AssetFilterBar
      total={total}
      search={search}
      onSearchChange={() => {}}
      placeholder="Search..."
      searchTestId="search"
      createdBy={{ value: creator, onChange: () => {}, myUserId: 1, options, testId: 'created-by' }}
      onClear={() => {}}
      clearTestId="clear"
    />,
  )
}

// Holds the search like a page does; the other filters count as cleared once onClear runs.
function Page() {
  const [search, setSearch] = useState('legal')
  const [cleared, setCleared] = useState(false)
  return (
    <>
      <AssetFilterBar total={2} search={search} onSearchChange={setSearch} placeholder="Search..." searchTestId="search" onClear={() => setCleared(true)} clearTestId="clear" />
      {cleared && <p>other filters cleared</p>}
    </>
  )
}

describe('AssetFilterBar', () => {
  it('Clear filters empties the search box and clears the other filters', () => {
    render(<Page />)
    fireEvent.click(screen.getByTestId('clear'))
    expect(screen.getByTestId('search')).toHaveValue('')
    expect(screen.getByText('other filters cleared')).toBeInTheDocument()
  })

  it('an empty list with no filter shows no bar', () => {
    renderBar({ total: 0 })
    expect(screen.queryByTestId('search')).not.toBeInTheDocument()
  })

  it('an empty list with a filter set keeps the bar, so the filter can be cleared', () => {
    renderBar({ total: 0, search: 'legal' })
    expect(screen.getByTestId('search')).toHaveValue('legal')
    expect(screen.getByTestId('clear')).toHaveTextContent('Clear filters (1)')
  })

  it('hides the Created by chip with one creator and no value', () => {
    renderBar({ options: [ANA] })
    expect(screen.queryByTestId('created-by')).not.toBeInTheDocument()
  })

  it('shows the Created by chip with two or more creators', () => {
    renderBar({ options: [ANA, BEN] })
    expect(screen.getByTestId('created-by')).toBeInTheDocument()
  })

  it('shows the Created by chip for a value set in the URL, even with one creator', () => {
    renderBar({ options: [ANA], creator: 2 })
    expect(screen.getByTestId('created-by')).toBeInTheDocument()
  })
})
