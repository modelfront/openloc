// The Status column's icons: which status gets which icon, and that each icon
// carries its name for screen readers and on hover.
import type { ReactElement } from 'react'
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SegmentStatusIcon, LockIcon, statusIconFor } from './SegmentStatusIcon'

afterEach(cleanup)

describe('statusIconFor', () => {
  it('marks the progressed states: Draft, Approved, Auto-approved', () => {
    expect(statusIconFor('needs_review', { status: 'needs_review' })).toEqual({ kind: 'draft', label: 'Draft' })
    expect(statusIconFor('approved', { status: 'approved', confirmed_via: 'user' })).toEqual({ kind: 'approved', label: 'Approved' })
    expect(statusIconFor('auto_approved', { status: 'auto_approved' })).toEqual({ kind: 'machine_approved', label: 'Auto-approved' })
  })

  it('gives a segment the assistant confirmed the machine-approved icon, under its own name', () => {
    expect(statusIconFor('approved', { status: 'approved', confirmed_via: 'assistant' }))
      .toEqual({ kind: 'machine_approved', label: 'Confirmed by assistant' })
  })

  it('shows nothing for the default states', () => {
    expect(statusIconFor('untranslated', null)).toBeNull()
    expect(statusIconFor('pre_translated', { status: 'needs_review' })).toBeNull()
  })
})

describe('SegmentStatusIcon', () => {
  const renderIcon = (ui: ReactElement) => render(<TooltipProvider>{ui}</TooltipProvider>)

  it('is an image named after its status, and shows the name on hover', async () => {
    renderIcon(<SegmentStatusIcon kind="draft" label="Draft" />)
    const icon = screen.getByRole('img', { name: 'Draft' })
    expect(icon).toHaveAttribute('data-status-icon', 'draft')

    await userEvent.hover(icon)
    expect(await screen.findByText('Draft', { selector: ':not(svg)' })).toBeInTheDocument()
  })

  it('colours by state: yellow for a draft, green for both approvals', () => {
    renderIcon(
      <>
        <SegmentStatusIcon kind="draft" label="Draft" />
        <SegmentStatusIcon kind="approved" label="Approved" />
        <SegmentStatusIcon kind="machine_approved" label="Auto-approved" />
      </>,
    )
    expect(screen.getByRole('img', { name: 'Draft' }).getAttribute('class')).toContain('text-yellow-500')
    expect(screen.getByRole('img', { name: 'Approved' }).getAttribute('class')).toContain('text-green-600')
    expect(screen.getByRole('img', { name: 'Auto-approved' }).getAttribute('class')).toContain('text-green-600')
  })

  it('draws Approved and Auto-approved differently: only the machine approval has sparkles', () => {
    renderIcon(
      <>
        <SegmentStatusIcon kind="approved" label="Approved" />
        <SegmentStatusIcon kind="machine_approved" label="Auto-approved" />
      </>,
    )
    const paths = (name: string) => screen.getByRole('img', { name }).querySelectorAll('path').length
    expect(paths('Auto-approved')).toBe(paths('Approved') + 2)
  })

  it('greys out a previous step\'s state, keeping the shape', () => {
    renderIcon(
      <>
        <SegmentStatusIcon kind="machine_approved" label="Now" />
        <SegmentStatusIcon kind="machine_approved" label="Before" history />
      </>,
    )
    const now = screen.getByRole('img', { name: 'Now' })
    const before = screen.getByRole('img', { name: 'Before' })
    expect(before.getAttribute('class')).toContain('text-gray-400')
    expect(before.getAttribute('class')).not.toContain('text-green')
    expect(before.innerHTML).toBe(now.innerHTML)
  })
})

describe('LockIcon', () => {
  it('is an image named Locked', async () => {
    render(<TooltipProvider><LockIcon /></TooltipProvider>)
    const icon = screen.getByRole('img', { name: 'Locked' })
    await userEvent.hover(icon)
    expect(await screen.findByText('Locked', { selector: ':not(svg)' })).toBeInTheDocument()
  })
})
