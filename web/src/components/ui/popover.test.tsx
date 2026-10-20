import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from './popover'
import { Button } from './button'

// Pins the `size` variant's class shape the way chip.test.tsx pins Chip sizes:
// the three compact call sites (QA dot, regex help, sidebar customizer) no
// longer carry padding / text-size / title-weight classes of their own, so a
// regression here would go unnoticed by their aria-only tests.
async function openPopover(size?: 'default' | 'compact') {
  render(
    <Popover>
      <PopoverTrigger render={<Button>Open</Button>} />
      <PopoverContent size={size} data-testid="popup">
        <PopoverTitle>Title</PopoverTitle>
      </PopoverContent>
    </Popover>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Open' }))
  return screen.findByRole('dialog')
}

describe('PopoverContent size', () => {
  it('default: 14px body, default padding, medium title', async () => {
    const dialog = await openPopover()
    expect(dialog.dataset.size).toBe('default')
    expect(dialog.className).toContain('text-sm')
    expect(dialog.className).toContain('p-2.5')
    expect(dialog.className).not.toContain('text-xs')
    // The title's weight follows data-size on the popup, so it needs no class of its own.
    expect(dialog.className).not.toContain('font-semibold')
  })

  it('compact: 12px body, tighter padding, title switches to semibold via data-size', async () => {
    const dialog = await openPopover('compact')
    expect(dialog.dataset.size).toBe('compact')
    for (const cls of ['gap-2', 'px-3', 'py-2.5', 'text-xs']) expect(dialog.className).toContain(cls)
    expect(dialog.className).not.toContain('text-sm')
    const title = screen.getByText('Title')
    expect(title.className).toContain('in-data-[size=compact]:font-semibold')
  })
})
