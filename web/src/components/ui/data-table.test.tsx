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

describe('DataTable sortable columns', () => {
  const onToggle = vi.fn()
  function Multi() {
    const columns: ColumnDef<Row>[] = [
      { id: 'name', header: 'Name', cell: ({ row }) => row.original.name, meta: { sort: { direction: null, onToggle: (s: boolean) => onToggle('name', s) } } },
      { id: 'first', header: 'First', size: 100, cell: () => 'x', meta: { sort: { direction: 'asc', rank: 1, onToggle: (s: boolean) => onToggle('first', s) } } },
      { id: 'second', header: 'Second', size: 100, cell: () => 'y', meta: { sort: { direction: 'desc', rank: 2, onToggle: (s: boolean) => onToggle('second', s) } } },
    ]
    return <DataTable columns={columns} data={[{ name: 'a' }]} />
  }

  it('offers a sort on a column the list is not sorted by, without aria-sort', () => {
    render(<Multi />)
    expect(screen.getByRole('columnheader', { name: /Name/ })).not.toHaveAttribute('aria-sort')
    expect(screen.getByTestId('sort-name')).toBeInTheDocument()
    expect(screen.queryByTestId('sort-rank-name')).not.toBeInTheDocument()
  })

  it('numbers the levels and puts aria-sort on the first level only', () => {
    render(<Multi />)
    expect(screen.getByRole('columnheader', { name: /First/ })).toHaveAttribute('aria-sort', 'ascending')
    expect(screen.getByRole('columnheader', { name: /Second/ })).not.toHaveAttribute('aria-sort')
    expect(screen.getByTestId('sort-rank-first')).toHaveTextContent(/^1sorted 1st, ascending$/)
    expect(screen.getByTestId('sort-rank-second')).toHaveTextContent(/^2sorted 2nd, descending$/)
  })

  it('tells the caller whether Shift was held', () => {
    render(<Multi />)
    fireEvent.click(screen.getByTestId('sort-name'))
    fireEvent.click(screen.getByTestId('sort-second'), { shiftKey: true })
    expect(onToggle.mock.calls).toEqual([['name', false], ['second', true]])
  })

  it("draws the arrow the caller names over the one the direction implies", () => {
    const columns: ColumnDef<Row>[] = [
      { id: 'plain', header: 'Plain', cell: () => 'a', meta: { sort: { direction: 'asc', onToggle } } },
      { id: 'natural', header: 'Natural', cell: () => 'b', meta: { sort: { direction: 'asc', arrow: 'down', onToggle } } },
    ]
    render(<DataTable columns={columns} data={[{ name: 'a' }]} />)
    expect(screen.getByTestId('sort-plain').querySelector('.lucide-arrow-up')).not.toBeNull()
    expect(screen.getByTestId('sort-natural').querySelector('.lucide-arrow-down')).not.toBeNull()
    // aria-sort still tells the values' order.
    expect(screen.getByRole('columnheader', { name: /Natural/ })).toHaveAttribute('aria-sort', 'ascending')
  })
})

describe('DataTable row clicks', () => {
  const columns: ColumnDef<Row>[] = [
    { id: 'name', header: 'Name', cell: ({ row }) => row.original.name },
    { id: 'open', header: 'Open', cell: () => <a href="/elsewhere" data-testid="cell-link">Open</a> },
  ]

  it('passes the click event to onRowClick', () => {
    const onRowClick = vi.fn()
    render(<DataTable columns={columns} data={[{ name: 'a' }]} onRowClick={onRowClick} rowTestId={r => `row-${r.name}`} />)
    fireEvent.click(screen.getByTestId('row-a'), { metaKey: true })
    expect(onRowClick).toHaveBeenCalledTimes(1)
    expect(onRowClick.mock.calls[0][0]).toEqual({ name: 'a' })
    expect(onRowClick.mock.calls[0][1].metaKey).toBe(true)
  })

  it('leaves a middle click alone unless the table takes it', () => {
    const onRowClick = vi.fn()
    render(<DataTable columns={columns} data={[{ name: 'a' }]} onRowClick={onRowClick} rowTestId={r => `row-${r.name}`} />)
    fireEvent(screen.getByTestId('row-a'), new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(onRowClick).not.toHaveBeenCalled()
  })

  it('takes a middle click on the row when asked, but not a right click nor one on a link in a cell', () => {
    const onRowAuxClick = vi.fn()
    render(<DataTable columns={columns} data={[{ name: 'a' }]} onRowClick={() => {}} onRowAuxClick={onRowAuxClick} rowTestId={r => `row-${r.name}`} />)
    const row = screen.getByTestId('row-a')
    fireEvent(row, new MouseEvent('auxclick', { bubbles: true, button: 2 }))
    fireEvent(screen.getByTestId('cell-link'), new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(onRowAuxClick).not.toHaveBeenCalled()
    fireEvent(row, new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(onRowAuxClick).toHaveBeenCalledTimes(1)
    expect(onRowAuxClick.mock.calls[0][0]).toEqual({ name: 'a' })
  })
})

// virtualRows: a long table mounts only the rows near the screen and still
// grows the page — its scroll container is the nearest scrolling ancestor.
describe('DataTable virtual rows', () => {
  const many = Array.from({ length: 1000 }, (_, i) => ({ name: `row ${i}` }))
  const nameColumn: ColumnDef<{ name: string }>[] = [{ id: 'name', header: 'Name', cell: ({ row }) => row.original.name }]

  function renderInScroller(props: Partial<Parameters<typeof DataTable<{ name: string }, unknown>>[0]>) {
    // jsdom has no layout: give the scroller a 400px viewport.
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return { top: 0, left: 0, right: 800, bottom: 400, width: 800, height: 400, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
    })
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(400)
    render(
      <div style={{ overflowY: 'auto', height: 400 }} data-testid="scroller">
        <DataTable columns={nameColumn} data={many} {...props} />
      </div>,
    )
    return () => { rect.mockRestore(); height.mockRestore() }
  }

  it('mounts only a window of rows', () => {
    const restore = renderInScroller({ virtualRows: { rowHeight: 40 } })
    const rows = screen.getAllByRole('row').filter(r => r.textContent?.startsWith('row '))
    expect(rows.length).toBeGreaterThan(0)
    expect(rows.length).toBeLessThan(60)
    expect(rows[0].textContent).toBe('row 0')
    restore()
  })

  it('mounts every row without it', () => {
    const restore = renderInScroller({})
    expect(screen.getAllByRole('row').filter(r => r.textContent?.startsWith('row '))).toHaveLength(1000)
    restore()
  })
})

describe('DataTable hideHeader', () => {
  it('keeps the column headers for screen readers and moves their widths to the columns', () => {
    const { container } = render(<DataTable columns={COLUMNS} data={[{ name: 'a' }]} hideHeader />)
    expect(screen.getAllByRole('columnheader').map(h => h.textContent)).toEqual(['Fixed', 'Wide', 'Rest'])
    expect(container.querySelector('thead')).toHaveClass('sr-only')
    // A sized column keeps its width; the others share what is left.
    expect([...container.querySelectorAll('col')].map(c => (c as HTMLElement).style.width)).toEqual(['100px', '', ''])
  })

  it('shows the header, with no column group, by default', () => {
    const { container } = render(<DataTable columns={COLUMNS} data={[{ name: 'a' }]} />)
    expect(container.querySelector('thead')).not.toHaveClass('sr-only')
    expect(container.querySelector('colgroup')).toBeNull()
  })
})
