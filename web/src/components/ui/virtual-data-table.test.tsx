import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import type { ColumnDef } from '@tanstack/react-table'

import { VirtualDataTable } from './virtual-data-table'

interface Row { name: string }

// Two sized columns and one fill column (no size).
const COLUMNS: ColumnDef<Row>[] = [
  { id: 'code', header: 'Code', size: 70, cell: () => 'x' },
  { id: 'name', header: 'Name', cell: () => 'x' },
  { id: 'info', header: 'Info', size: 280, cell: () => 'x' },
]

// Below md the table may not get narrower than this; it scrolls sideways instead.
function minWidth(container: HTMLElement) {
  const box = container.querySelector<HTMLElement>('[style*="--table-min-width"]')
  return box?.style.getPropertyValue('--table-min-width')
}

describe('VirtualDataTable phone min width', () => {
  afterEach(cleanup)

  it('sums the sized columns and a 160px floor per fill column', () => {
    const { container } = render(<VirtualDataTable columns={COLUMNS} data={[{ name: 'a' }]} />)
    expect(minWidth(container)).toBe('510px')
  })

  it('uses the floor the caller sets', () => {
    const { container } = render(<VirtualDataTable columns={COLUMNS} data={[{ name: 'a' }]} minFillColumnWidth={320} />)
    expect(minWidth(container)).toBe('670px')
  })
})
