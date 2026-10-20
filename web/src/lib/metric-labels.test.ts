import { describe, it, expect } from 'vitest'
import { metricLabel } from './metric-labels'

describe('history kind vs translation origin', () => {
  it('labels the two groupings differently', () => {
    expect(metricLabel('kind')).toBe('Change type')
    expect(metricLabel('origin')).toBe('Origin')
  })
})

describe('history measures', () => {
  it('are named for what they count', () => {
    expect(metricLabel('words_processed')).toBe('Post-editing volume')
    expect(metricLabel('segments_by_origin')).toBe('Translation activity by type')
    expect(metricLabel('source_volume')).toBe('Source volume')
    expect(metricLabel('editing_time')).toBe('Post-editing time')
    expect(metricLabel('post_edit_effort')).toBe('Post-editing effort')
  })
})
