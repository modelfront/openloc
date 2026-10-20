import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CreatorLabel } from './CreatorLabel'

describe('CreatorLabel', () => {
  it('renders the creator name', () => {
    render(<CreatorLabel name="Ana Ruiz" />)
    expect(screen.getByTestId('creator-label')).toHaveTextContent('Ana Ruiz')
  })

  it.each([
    ['undefined', undefined],
    ['null', null],
    ['empty string', ''],
    ['whitespace only', '   '],
  ])('falls back to the placeholder when the name is %s', (_label, name) => {
    render(<CreatorLabel name={name} />)
    expect(screen.getByTestId('creator-label-empty')).toHaveTextContent('—')
    expect(screen.queryByTestId('creator-label')).toBeNull()
  })

  it('honours a custom placeholder', () => {
    render(<CreatorLabel name="" emptyText="Unknown" />)
    expect(screen.getByTestId('creator-label-empty')).toHaveTextContent('Unknown')
  })
})
