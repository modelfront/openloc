import { describe, it, expect } from 'vitest'
import { dropMove } from './SortableList'

// Dragging is dnd-kit (pointer and keyboard sensors) and needs layout, which
// jsdom does not do; the move a drop makes is dropMove.
describe('dropMove', () => {
  const ids = ['a', 'b', 'c']

  it('reports the positions a drop moves between, up or down', () => {
    expect(dropMove(ids, 'c', 'a')).toEqual([2, 0])
    expect(dropMove(ids, 'a', 'b')).toEqual([0, 1])
  })

  it('moves nothing for a drop on itself, outside the list, or an unknown id', () => {
    expect(dropMove(ids, 'b', 'b')).toBeNull()
    expect(dropMove(ids, 'b', null)).toBeNull()
    expect(dropMove(ids, 'gone', 'b')).toBeNull()
    expect(dropMove(ids, 'b', 'gone')).toBeNull()
  })
})
