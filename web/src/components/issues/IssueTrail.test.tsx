import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { IssueTrail } from './IssueTrail'
import { keptInCapture } from '@/lib/screenshot'

const events = [{ at: '2026-10-17T10:00:00Z', kind: 'click' as const, target: 'Save' }]

describe('IssueTrail', () => {
  // A list of the user's actions in time order never goes to a model.
  it('stays out of every screenshot, open or closed', () => {
    render(<IssueTrail events={events} label="Recent actions" testId="trail" />)
    const root = screen.getByTestId('trail')
    expect(keptInCapture(root)).toBe(false)
    fireEvent.click(screen.getByTestId('trail-toggle'))
    expect(screen.getByTestId('trail-events')).toBeInTheDocument()
    expect(keptInCapture(screen.getByTestId('trail'))).toBe(false)
  })
})
