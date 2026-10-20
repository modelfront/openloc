import { describe, expect, it } from 'vitest'

import { clickSelect, headerState, selectionLabel, setMany, visibleSelection } from './segment-selection'
import { bulkSummary, chunk, mapChunks, mergeResults, SEGMENT_BULK_MAX } from './segment-bulk'

const ordered = [10, 20, 30, 40, 50]
const ids = (s: Set<number>) => [...s].sort((a, b) => a - b)

describe('clickSelect', () => {
  it('toggles one row on a plain click', () => {
    expect(ids(clickSelect(new Set(), ordered, 30, null, false))).toEqual([30])
    expect(ids(clickSelect(new Set([30, 40]), ordered, 30, 40, false))).toEqual([40])
  })

  it('selects the range between anchor and click on shift, in display order', () => {
    expect(ids(clickSelect(new Set([20]), ordered, 40, 20, true))).toEqual([20, 30, 40])
    // Backwards range.
    expect(ids(clickSelect(new Set([40]), ordered, 10, 40, true))).toEqual([10, 20, 30, 40])
  })

  it('a shift range takes the state the clicked row switches to', () => {
    // 40 is selected, so shift-clicking it clears the whole range.
    expect(ids(clickSelect(new Set([10, 20, 30, 40, 50]), ordered, 40, 20, true))).toEqual([10, 50])
  })

  it('falls back to a plain toggle when the anchor is not on screen', () => {
    expect(ids(clickSelect(new Set([99]), ordered, 30, 99, true))).toEqual([30, 99])
  })

  it('keeps rows hidden by the filter when a range runs over the visible ones', () => {
    expect(ids(clickSelect(new Set([99, 10]), ordered, 30, 10, true))).toEqual([10, 20, 30, 99])
  })
})

describe('visibility', () => {
  const selected = new Set([20, 40, 99])

  it('acts only on selected rows on screen, in display order', () => {
    expect(visibleSelection(selected, ordered)).toEqual([20, 40])
  })

  it('header reflects only the rows on screen', () => {
    expect(headerState(new Set([99]), ordered)).toBe('none')
    expect(headerState(selected, ordered)).toBe('some')
    expect(headerState(setMany(new Set([99]), ordered, true), ordered)).toBe('all')
    expect(headerState(new Set(), [])).toBe('none')
  })

  it('label counts hidden rows', () => {
    expect(selectionLabel(1, 1)).toBe('1 segment selected')
    expect(selectionLabel(2, 3)).toBe('2 segments selected · 1 hidden by filter')
  })
})

describe('bulk result', () => {
  it('chunks by size', () => {
    expect(chunk([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]])
  })

  it('mapChunks runs one chunk at a time, in order', async () => {
    const ids = Array.from({ length: SEGMENT_BULK_MAX + 1 }, (_, i) => i)
    let running = 0
    let maxRunning = 0
    const sizes = await mapChunks(ids, async part => {
      running++
      maxRunning = Math.max(maxRunning, running)
      await new Promise(r => setTimeout(r, 1))
      running--
      return part.length
    })
    expect(sizes).toEqual([SEGMENT_BULK_MAX, 1])
    expect(maxRunning).toBe(1)
  })

  it('summarises applied and each skip reason', () => {
    const res = mergeResults([
      { applied: [1, 2], skipped: [{ id: 3, reason: 'unchanged' }] },
      { applied: [4], skipped: [{ id: 5, reason: 'unchanged' }, { id: 6, reason: 'not_found' }] },
    ])
    expect(bulkSummary('lock', res)).toBe('Locked 3 segments · 2 already locked · 1 not in this task')
    expect(bulkSummary('clear', { applied: [1], skipped: [{ id: 2, reason: 'locked' }, { id: 3, reason: 'empty' }] }))
      .toBe('Cleared 1 segment · 1 locked · 1 already empty')
    expect(bulkSummary('confirm', { applied: [1], skipped: [{ id: 2, reason: 'held' }] }))
      .toBe('Confirmed 1 segment · 1 being edited by someone else')
    expect(bulkSummary('confirm', {
      applied: [1],
      skipped: [{ id: 2, reason: 'qa_error' }, { id: 3, reason: 'auto_approved' }, { id: 4, reason: 'unchanged' }, { id: 5, reason: 'conflict' }],
    })).toBe('Confirmed 1 segment · 1 with QA errors · 1 auto-approved (confirm one by one) · 1 already confirmed · 1 changed meanwhile')
    expect(bulkSummary('confirm', { applied: [], skipped: [{ id: 2, reason: 'tag_not_in_source' }] }))
      .toBe('Confirmed 0 segments · 1 with a tag the source does not have')
  })

  it('merges reopen flags across chunks', () => {
    expect(mergeResults([{ applied: [], skipped: [] }, { applied: [1], skipped: [], task_reopened: true }]).task_reopened).toBe(true)
  })
})
