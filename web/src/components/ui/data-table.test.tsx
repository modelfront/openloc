// DataTable's column resizing: what a drag does to the widths it hands back.
// jsdom has no layout, so the two sizes the table measures — a header's shown
// width and the box's width — are stubbed per test; the real drag is covered
// by e2e/tests/task-matrix.spec.ts.

import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ColumnSizingState } from '@tanstack/react-table'

import { DataTable, type ColumnDef } from './data-table'

interface Row { name: string }

// `fixed` has a size; `wide` and `rest` share what is left. Only the first two
// may be dragged.
const COLUMNS: ColumnDef<Row>[] = [
  { id: 'fixed', header: 'Fixed', size: 100, enableResizing: true, minSize: 50, maxSize: 2000, cell: () => 'x' },
  { id: 'wide', header: 'Wide', enableResizing: true, minSize: 50, maxSize: 2000, cell: () => 'x' },
  { id: 'rest', header: 'Rest', cell: () => 'x' },
]

function Harness({ initial = {}, resizable = true }: { initial?: ColumnSizingState; resizable?: boolean }) {
  const [sizing, setSizing] = useState<ColumnSizingState>(initial)
  return (
    <DataTable
      columns={COLUMNS}
      data={[{ name: 'a' }]}
      {...(resizable && { columnSizing: sizing, onColumnSizingChange: setSizing })}
    />
  )
}

const handle = (id: string) => screen.getByRole('separator', { name: `Resize ${id} column` })
const width = (id: string) => (handle(id).parentElement as HTMLElement).style.width

function drag(id: string, dx: number) {
  fireEvent.mouseDown(handle(id), { clientX: 500 })
  fireEvent.mouseMove(document.body, { clientX: 500 + dx })
  fireEvent.mouseUp(document.body, { clientX: 500 + dx })
}

/** Every header shows `shown` px wide; the table's box is `box` px. */
function stubLayout({ shown = 0, box = 0 }: { shown?: number; box?: number }) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
    { width: shown, height: 20, x: 0, y: 0, top: 0, left: 0, right: shown, bottom: 20, toJSON: () => ({}) },
  )
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(box)
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('DataTable column resizing', () => {
  it('gives a drag strip only to the columns that ask for one, and none without a sizing handler', () => {
    const view = render(<Harness />)
    expect(screen.getAllByRole('separator').map(s => s.getAttribute('aria-label')))
      .toEqual(['Resize fixed column', 'Resize wide column'])
    view.unmount()
    render(<Harness resizable={false} />)
    expect(screen.queryAllByRole('separator')).toHaveLength(0)
  })

  it('a sized column drags from its size', () => {
    render(<Harness />)
    expect(width('fixed')).toBe('100px')
    drag('fixed', 40)
    expect(width('fixed')).toBe('140px')
  })

  it('a column sharing the space drags from the width it shows, not a default', () => {
    stubLayout({ shown: 300 })
    render(<Harness />)
    expect(width('wide')).toBe('') // shares the space: no width of its own
    drag('wide', 60)
    expect(width('wide')).toBe('360px')
  })

  it('a drag stops where the table would outgrow its box, leaving a shared column 120px', () => {
    // 800px box: fixed 100 + rest's 120 floor leaves wide at most 580.
    stubLayout({ shown: 300, box: 800 })
    render(<Harness />)
    drag('wide', 1000)
    expect(width('wide')).toBe('580px')
  })

  it('a table that no longer fits its box can still be narrowed', () => {
    // wide 900 already overflows an 800px box; narrowing by 100 still does.
    stubLayout({ box: 800 })
    render(<Harness initial={{ wide: 900 }} />)
    drag('wide', -100)
    expect(width('wide')).toBe('800px')
  })

  it('double-click puts a dragged column back: its size, or sharing the space', () => {
    render(<Harness initial={{ fixed: 250, wide: 400 }} />)
    fireEvent.doubleClick(handle('fixed'))
    fireEvent.doubleClick(handle('wide'))
    expect(width('fixed')).toBe('100px')
    expect(width('wide')).toBe('')
  })
})

describe('DataTable sorted column', () => {
  function Sorted() {
    const [direction, setDirection] = useState<'asc' | 'desc'>('desc')
    const columns: ColumnDef<Row>[] = [
      { id: 'name', header: 'Name', cell: ({ row }) => row.original.name },
      {
        id: 'updated', header: 'Updated', size: 100, cell: () => 'x',
        meta: { sort: { direction, onToggle: () => setDirection(d => (d === 'desc' ? 'asc' : 'desc')) } },
      },
    ]
    return <DataTable columns={columns} data={[{ name: 'a' }]} onRowClick={rowClick} />
  }
  const rowClick = vi.fn()

  it('marks the sorted header and toggles its direction from a button', () => {
    render(<Sorted />)
    const header = screen.getByRole('columnheader', { name: /Updated/ })
    expect(header).toHaveAttribute('aria-sort', 'descending')
    expect(screen.getByRole('columnheader', { name: 'Name' })).not.toHaveAttribute('aria-sort')
    fireEvent.click(screen.getByRole('button', { name: /Updated/ }))
    expect(header).toHaveAttribute('aria-sort', 'ascending')
    expect(rowClick).not.toHaveBeenCalled()
  })
})

describe('DataTable phone min width', () => {
  // A flexible column counts with its 120px floor, a sized or dragged one with
  // its width. Below md the table may not get narrower than that.
  const minWidth = () => screen.getByRole('table').style.getPropertyValue('--table-min-width')

  it('sums the fixed widths and a floor per flexible column', () => {
    render(<Harness resizable={false} />)
    expect(minWidth()).toBe('340px')
  })

  it('counts a dragged column at its dragged width', () => {
    render(<Harness initial={{ wide: 300 }} />)
    expect(minWidth()).toBe('520px')
  })
})
