import { describe, it, expect } from 'vitest'
import { formatDateTime } from '../utils'
import { tmEntryStamps, tmStampText } from './tm-entry-stamps'

const entry = {
  created_at: '2026-09-03T09:10:00Z',
  updated_at: '2026-10-12T14:32:00Z',
  created_by: 'Daniel Weber <daniel@test.com>',
  updated_by: 'Anna Miller <anna@test.com>',
}

describe('tmEntryStamps', () => {
  // The creator and the last editor are different people with different
  // dates; mixing the pairs up would credit the wrong person.
  it('keeps the creation and the last change apart', () => {
    expect(tmEntryStamps(entry)).toEqual({
      created: `${formatDateTime(entry.created_at)} · Daniel Weber <daniel@test.com>`,
      changed: `${formatDateTime(entry.updated_at)} · Anna Miller <anna@test.com>`,
    })
  })
})

describe('tmStampText', () => {
  it('is the date alone when no user was recorded', () => {
    expect(tmStampText(entry.updated_at, '')).toBe(formatDateTime(entry.updated_at))
  })
})
