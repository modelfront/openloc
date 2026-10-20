import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EditableTitle } from './EditableTitle'

describe('EditableTitle', () => {
  it('a read-only name takes keyboard focus by default, so its full text is reachable', () => {
    render(<EditableTitle name="Quarterly report" canEdit={false} />)
    expect(screen.getByText('Quarterly report')).toHaveAttribute('tabindex', '0')
  })

  it('focusable={false} leaves focus to the control around it', () => {
    render(<button type="button"><EditableTitle name="Quarterly report" canEdit={false} focusable={false} /></button>)
    expect(screen.getByText('Quarterly report')).not.toHaveAttribute('tabindex')
  })
})
