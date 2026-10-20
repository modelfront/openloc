import { describe, it, expect } from 'vitest'
import type { Label } from '../../api'
import { labelDeleteBlocker } from './label-delete'

const label = (values: [string, number][]): Label => ({
  id: 1,
  name: 'Country',
  system: false,
  in_use: values.some(([, n]) => n > 0),
  values: values.map(([value, n], i) => ({ id: i + 1, value, in_use: n > 0, project_count: n })),
})

describe('labelDeleteBlocker', () => {
  it('is null when no value is in use', () => {
    expect(labelDeleteBlocker(label([['Germany', 0], ['Japan', 0]]))).toBeNull()
    expect(labelDeleteBlocker(label([]))).toBeNull()
  })

  it('names the one value in use and its project count', () => {
    expect(labelDeleteBlocker(label([['Germany', 1], ['Japan', 0]]))).toBe(
      "Can't delete yet: Germany (1 project) is still in use. Delete that value first, or change the projects.",
    )
  })

  it('lists several values and leaves out the unused ones', () => {
    expect(labelDeleteBlocker(label([['Germany', 2], ['Japan', 0], ['Spain', 1]]))).toBe(
      "Can't delete yet: Germany (2 projects) and Spain (1 project) are still in use. Delete those values first, or change the projects.",
    )
  })

  it('caps the list and counts the rest', () => {
    expect(labelDeleteBlocker(label([['A', 1], ['B', 1], ['C', 1], ['D', 1], ['E', 1]]))).toBe(
      "Can't delete yet: A (1 project), B (1 project), C (1 project) and 2 more are still in use. Delete those values first, or change the projects.",
    )
  })
})
