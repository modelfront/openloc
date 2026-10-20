import { describe, expect, it } from 'vitest'
import { formatFileSize } from './file-size'

describe('formatFileSize', () => {
  it.each([
    [0, '0 B'],
    [1023, '1023 B'],
    [1024, '1.0 KB'],
    [3500, '3.4 KB'],
    [25 * 1024 * 1024, '25 MB'],
    [3 * 1024 ** 3, '3.0 GB'],
  ])('%d bytes → %s', (bytes, want) => {
    expect(formatFileSize(bytes)).toBe(want)
  })
})
