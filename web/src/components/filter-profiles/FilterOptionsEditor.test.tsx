import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)
vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return {
    ...actual,
    listLanguages: vi.fn().mockResolvedValue([{ code: 'de', name: 'German' }, { code: 'es', name: 'Spanish' }, { code: 'fr', name: 'French' }]),
  }
})
import type { FilterFormat } from '@/lib/filter-formats'
import { valuesOf, type FilterOptionValues } from '@/lib/filter-options'
import { FilterOptionsEditor } from './FilterOptionsEditor'

function Harness({ format, readOnly = false, onValues }: {
  format: FilterFormat; readOnly?: boolean; onValues?: (v: FilterOptionValues) => void
}) {
  const [value, setValue] = useState(valuesOf(undefined))
  const [qc] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }))
  return (
    <QueryClientProvider client={qc}>
      <FilterOptionsEditor format={format} value={value} readOnly={readOnly}
        onChange={patch => setValue(v => { const next = { ...v, ...patch }; onValues?.(next); return next })} />
    </QueryClientProvider>
  )
}

describe('FilterOptionsEditor', () => {
  it('offers only the HTML switch for JSON, PO and plain text', () => {
    let latest: FilterOptionValues | undefined
    render(<Harness format="po" onValues={v => { latest = v }} />)
    expect(screen.queryByTestId('filter-xlsx-rules')).toBeNull()
    const box = screen.getByTestId('filter-inline-html') as HTMLInputElement
    expect(box.checked).toBe(true)
    fireEvent.click(box)
    expect(latest?.inlineHTML).toBe(false)
  })

  it('adds the Excel rules for Excel', () => {
    let latest: FilterOptionValues | undefined
    render(<Harness format="xlsx" onValues={v => { latest = v }} />)
    expect(screen.getByTestId('filter-inline-html')).toBeTruthy()
    fireEvent.click(screen.getByTestId('filter-xlsx-header-row'))
    expect(latest?.headerRow).toBe(true)
  })

  it('adds, fills and deletes Excel target columns, offering each language once', async () => {
    const user = userEvent.setup()
    let latest: FilterOptionValues | undefined
    render(<Harness format="xlsx" onValues={v => { latest = v }} />)
    expect(screen.queryByTestId('filter-xlsx-target-0')).toBeNull()
    fireEvent.click(screen.getByTestId('filter-xlsx-target-add'))
    fireEvent.click(screen.getByTestId('filter-xlsx-target-add'))
    const [first, second] = screen.getAllByLabelText('Language') as HTMLSelectElement[]
    await screen.findAllByRole('option', { name: 'French' })
    await user.selectOptions(first, 'fr')
    await user.type(screen.getByTestId('filter-xlsx-target-column-0'), 'c')
    expect(latest?.targetColumns).toEqual([{ language: 'fr', column: 'C' }, { language: '', column: '' }])
    // The second row no longer offers French.
    expect([...second.options].map(o => o.value)).not.toContain('fr')
    await user.selectOptions(second, 'de')
    fireEvent.click(screen.getByTestId('filter-xlsx-target-delete-0'))
    expect(latest?.targetColumns).toEqual([{ language: 'de', column: '' }])
  })

  it('shows the HTML options, starting from the defaults', () => {
    let latest: FilterOptionValues | undefined
    render(<Harness format="html" onValues={v => { latest = v }} />)
    expect(screen.queryByTestId('filter-inline-html')).toBeNull()
    expect((screen.getByTestId('filter-html-meta') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByTestId('filter-html-skip-pre') as HTMLInputElement).checked).toBe(true)
    expect(screen.getAllByLabelText('Element').map(e => (e as HTMLInputElement).value)).toEqual(['a', 'abbr', 'img', 'input', 'td', 'th'])
    fireEvent.click(screen.getByTestId('filter-html-skip-pre'))
    expect(latest?.skipPre).toBe(false)
    fireEvent.click(screen.getByTestId('filter-html-add'))
    expect(latest?.htmlAttributes.at(-1)).toEqual({ element: '', attributes: [] })
    fireEvent.click(screen.getByTestId('filter-html-row-delete-0'))
    expect(latest?.htmlAttributes.map(r => r.element)).toEqual(['abbr', 'img', 'input', 'td', 'th', ''])
  })

  it('shows the XML lists', () => {
    render(<Harness format="xml" />)
    expect(screen.getByTestId('filter-xml-options')).toBeTruthy()
    for (const id of ['filter-xml-skip', 'filter-xml-no-translate', 'filter-xml-inline', 'filter-xml-ref-elements', 'filter-xml-ref-attributes']) {
      expect(screen.getByTestId(id)).toBeTruthy()
    }
    expect(screen.queryByTestId('filter-inline-html')).toBeNull()
  })

  it('disables every control when read-only', () => {
    render(<Harness format="xlsx" readOnly />)
    expect((screen.getByTestId('filter-inline-html') as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByTestId('filter-xlsx-header-row') as HTMLInputElement).disabled).toBe(true)
    expect(screen.queryByTestId('filter-xlsx-target-add')).toBeNull()
  })
})
