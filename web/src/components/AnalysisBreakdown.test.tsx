import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { JobAnalysis } from '../api/analyses'
import AnalysisBreakdown, { FILE_COLUMN_DEFAULT, NUMBER_COLUMN_MIN, NUMBER_COLUMNS } from './AnalysisBreakdown'

// Counts how often a file name renders, to tell a resize from a re-render of
// the rows.
const titleRenders = vi.hoisted(() => ({ count: 0 }))
vi.mock('@/components/list/EditableTitle', async importOriginal => {
  const real = await importOriginal<typeof import('@/components/list/EditableTitle')>()
  return {
    ...real,
    EditableTitle: (props: Parameters<typeof real.EditableTitle>[0]) => {
      titleRenders.count++
      return real.EditableTitle(props)
    },
  }
})

const LONG = `${'quarterly-report-final-version'.repeat(4)}.docx`

const row = (id: number, name: string, lang = 'de'): JobAnalysis =>
  ({ id, job_id: id, job_name_snapshot: name, target_language: lang, new_words: 100 }) as unknown as JobAnalysis

const table = () => screen.getAllByRole('table')[0]
const handle = () => screen.getAllByRole('separator', { name: 'Resize file column' })[0]

beforeEach(() => { localStorage.clear(); titleRenders.count = 0 })

describe('AnalysisBreakdown file column', () => {
  it('heads each table with its language pair', () => {
    render(<AnalysisBreakdown rows={[row(1, 'a.txt', 'de'), row(2, 'b.txt', 'fr')]} metric="words" sourceLanguage="en" />)
    expect(screen.getByRole('heading', { name: /en → de/i })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /en → fr/i })).toBeInTheDocument()
  })

  it('the file column shows it can be resized: a visible grip on its edge, with a hint', () => {
    render(<AnalysisBreakdown rows={[row(1, 'a.txt')]} metric="words" sourceLanguage="en" />)
    expect(handle().querySelector('svg')).not.toBeNull()
    expect(handle()).toHaveAttribute('title', 'Drag to resize, double-click to reset')
  })

  it('starts narrow enough to fit: the table needs no more than the default file width plus the number columns', () => {
    render(<AnalysisBreakdown rows={[row(1, LONG)]} metric="words" sourceLanguage="en" />)
    expect(table()).toHaveClass('w-full')
    expect(table()).toHaveStyle({ minWidth: `${FILE_COLUMN_DEFAULT + NUMBER_COLUMNS * NUMBER_COLUMN_MIN}px` })
    // The long name is clipped in its cell, not laid out in full.
    expect(screen.getByText(LONG)).toHaveClass('truncate')
  })

  it('widening the file column raises the table minimum, so it scrolls only then', () => {
    render(<AnalysisBreakdown rows={[row(1, LONG)]} metric="words" sourceLanguage="en" />)
    fireEvent.keyDown(handle(), { key: 'ArrowRight' })
    fireEvent.keyDown(handle(), { key: 'ArrowRight' })
    const widened = FILE_COLUMN_DEFAULT + 32
    expect(table()).toHaveStyle({ minWidth: `${widened + NUMBER_COLUMNS * NUMBER_COLUMN_MIN}px` })
    expect(table().querySelector('col')).toHaveStyle({ width: `${widened}px` })
  })

  it('every language table follows the same file width', () => {
    render(<AnalysisBreakdown rows={[row(1, 'a.txt', 'de'), row(2, 'b.txt', 'fr')]} metric="words" sourceLanguage="en" />)
    fireEvent.keyDown(handle(), { key: 'ArrowRight' })
    const cols = screen.getAllByRole('table').map(t => (t.querySelector('col') as HTMLElement).style.width)
    expect(cols).toEqual([`${FILE_COLUMN_DEFAULT + 16}px`, `${FILE_COLUMN_DEFAULT + 16}px`])
  })

  it('double-click on the handle restores the default width', () => {
    render(<AnalysisBreakdown rows={[row(1, LONG)]} metric="words" sourceLanguage="en" />)
    fireEvent.keyDown(handle(), { key: 'ArrowRight' })
    fireEvent.doubleClick(handle())
    expect(table().querySelector('col')).toHaveStyle({ width: `${FILE_COLUMN_DEFAULT}px` })
  })

  it('resizing the column re-renders no rows, however many there are', () => {
    const rows = Array.from({ length: 40 }, (_, i) => row(i + 1, `file-${i}.docx`, i % 2 ? 'de' : 'fr'))
    render(<AnalysisBreakdown rows={rows} metric="words" sourceLanguage="en" />)
    const before = titleRenders.count
    expect(before).toBeGreaterThanOrEqual(40)
    for (let i = 0; i < 5; i++) fireEvent.keyDown(handle(), { key: 'ArrowRight' })
    expect(table().querySelector('col')).toHaveStyle({ width: `${FILE_COLUMN_DEFAULT + 80}px` })
    expect(titleRenders.count).toBe(before)
  })
})
