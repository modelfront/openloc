import { describe, it, expect } from 'vitest'
import { errorText } from './error-text'

describe('errorText', () => {
  it("uses the error's own message", () => {
    expect(errorText(new Error('Email already invited'), 'Failed')).toBe('Email already invited')
  })

  it('falls back when there is no message to show', () => {
    expect(errorText(new Error(''), 'Failed')).toBe('Failed')
    expect(errorText('boom', 'Failed')).toBe('Failed')
    expect(errorText(undefined, 'Failed')).toBe('Failed')
  })
})
