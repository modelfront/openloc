import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MissingTagsBar from './MissingTagsBar'
import type { MissingTag } from '../lib/missing-tags'

afterEach(() => cleanup())

// Chips derive their label from these attrs at render time, so
// the fixtures carry identity only — no display text.
const bpt1: MissingTag = { tagType: 'bpt', tagId: '1', ctype: '', rawXml: '<bpt id="1">&lt;b&gt;</bpt>' }
const ept1: MissingTag = { tagType: 'ept', tagId: '1', ctype: '', rawXml: '<ept id="1">&lt;/b&gt;</ept>', closing: true }
const ph4:  MissingTag = { tagType: 'ph',  tagId: '4', ctype: '', rawXml: '<ph id="4"/>' }

describe('MissingTagsBar', () => {
  it('renders nothing when missing list is empty (no visual noise on a clean target)', () => {
    const { container } = render(<MissingTagsBar missing={[]} onInsert={() => {}} onInsertAll={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders one chip per missing tag and DOES NOT show "Insert all" when only one is missing', () => {
    render(<MissingTagsBar missing={[ph4]} onInsert={() => {}} onInsertAll={() => {}} />)
    expect(screen.getByTestId('missing-tags-bar')).toBeInTheDocument()
    expect(screen.getAllByTestId('missing-tag-chip')).toHaveLength(1)
    expect(screen.getByText('{4}')).toBeInTheDocument()
    // "Insert all" with 1 missing is just clutter — must be hidden.
    expect(screen.queryByTestId('missing-tags-insert-all')).not.toBeInTheDocument()
  })

  it('shows "Insert all" only when 2+ tags are missing', () => {
    render(<MissingTagsBar missing={[bpt1, ept1]} onInsert={() => {}} onInsertAll={() => {}} />)
    expect(screen.getAllByTestId('missing-tag-chip')).toHaveLength(2)
    expect(screen.getByTestId('missing-tags-insert-all')).toBeInTheDocument()
  })

  it('clicking a chip calls onInsert with that exact tag (passing the rawXml so SegmentRow can insert as a chip atom)', async () => {
    const onInsert = vi.fn()
    render(<MissingTagsBar missing={[bpt1, ept1]} onInsert={onInsert} onInsertAll={() => {}} />)
    const user = userEvent.setup()
    await user.click(screen.getAllByTestId('missing-tag-chip')[1])
    expect(onInsert).toHaveBeenCalledTimes(1)
    expect(onInsert).toHaveBeenCalledWith(ept1)
  })

  it('clicking "Insert all" calls onInsertAll once (NOT once per tag)', async () => {
    const onInsertAll = vi.fn()
    render(<MissingTagsBar missing={[bpt1, ept1, ph4]} onInsert={() => {}} onInsertAll={onInsertAll} />)
    const user = userEvent.setup()
    await user.click(screen.getByTestId('missing-tags-insert-all'))
    expect(onInsertAll).toHaveBeenCalledTimes(1)
  })

  it('chips carry data-tag-type so the global `.xliff-tag[data-tag-type=…]` CSS lights them with the right color', () => {
    render(<MissingTagsBar missing={[bpt1, ph4]} onInsert={() => {}} onInsertAll={() => {}} />)
    const chips = screen.getAllByTestId('missing-tag-chip')
    expect(chips[0]).toHaveAttribute('data-tag-type', 'bpt')
    expect(chips[1]).toHaveAttribute('data-tag-type', 'ph')
  })
})

// SegmentRow gates the bar with `!effectiveReadOnly && missingTags.length > 0`,
// so locked / read-only segments never render the bar at all. That gating
// is in SegmentRow, not here — but if MissingTagsBar ever stops respecting
// the empty-list contract, the gate becomes the only line of defense.
// This test pins MissingTagsBar's own contract: empty list → empty DOM.
describe('MissingTagsBar — readOnly defense', () => {
  it('rendering with empty list produces no DOM (the guard SegmentRow relies on for read-only segments)', () => {
    const { container } = render(<MissingTagsBar missing={[]} onInsert={() => {}} onInsertAll={() => {}} />)
    expect(container.firstChild,
      'MissingTagsBar must return null on empty list — SegmentRow uses missingTags.length > 0 guard, but the component must also self-defend').toBeNull()
  })
})
