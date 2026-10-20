import { describe, it, expect, vi } from 'vitest'
import type { ReactNode, ComponentProps } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CreatedByFilterChip } from './CreatedByFilterChip'
import type { CreatorRef } from '../../lib/creators'

// The popover mechanics (base-ui portal + open state) aren't what this tests,
// and jsdom portals make them noisy — render the content inline so the
// assertions are about the option set and the chip's label resolution.
vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  PopoverTrigger: ({ children, ...rest }: ComponentProps<'button'>) => <button type="button" {...rest}>{children}</button>,
  PopoverContent: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
}))

const ME = 1
const CREATORS: CreatorRef[] = [
  { id: ME, name: 'Me Myself' },
  { id: 2, name: 'Ana Ruiz' },
  { id: 3, name: 'nameless@test.com' },
]

function renderChip(props: Partial<Parameters<typeof CreatedByFilterChip>[0]> = {}) {
  const onChange = props.onChange ?? vi.fn()
  render(
    <CreatedByFilterChip
      value={null}
      myUserId={ME}
      options={CREATORS}
      {...props}
      onChange={onChange}
    />,
  )
  return { onChange }
}

describe('CreatedByFilterChip', () => {
  it('offers Anyone, Me and each other creator (never the current user twice)', () => {
    renderChip()

    expect(screen.getByText('Anyone')).toBeInTheDocument()
    expect(screen.getByText('Me')).toBeInTheDocument()
    // The current user is covered by "Me" — their name isn't repeated.
    expect(screen.queryByText('Me Myself')).toBeNull()
    expect(screen.getByText('Ana Ruiz')).toBeInTheDocument()
    // A creator with no display name arrives pre-resolved to their email.
    expect(screen.getByText('nameless@test.com')).toBeInTheDocument()
  })

  // The whole point of taking options from creators rather than org members:
  // an option that can only ever return an empty list is noise. Pins that
  // members who created nothing are not offered.
  it('offers nobody who is absent from the creator list', () => {
    renderChip({ options: [{ id: 2, name: 'Ana Ruiz' }] })

    expect(screen.getByText('Ana Ruiz')).toBeInTheDocument()
    expect(screen.queryByText('Val Vendor')).toBeNull()
    expect(screen.queryByText('nameless@test.com')).toBeNull()
  })

  it('hides "Me" when the current user has created nothing here', () => {
    renderChip({ options: [{ id: 2, name: 'Ana Ruiz' }] })

    expect(screen.queryByText('Me')).toBeNull()
    expect(screen.getByText('Anyone')).toBeInTheDocument()
  })

  it('offers only Anyone when nothing in the list has a creator', () => {
    renderChip({ options: [] })

    expect(screen.getByText('Anyone')).toBeInTheDocument()
    expect(screen.queryByText('Me')).toBeNull()
  })

  it('reports the picked creator id, and null for Anyone', async () => {
    const { onChange } = renderChip()

    await userEvent.click(screen.getByText('Ana Ruiz'))
    expect(onChange).toHaveBeenCalledWith(2)

    await userEvent.click(screen.getByText('Me'))
    expect(onChange).toHaveBeenLastCalledWith(ME)

    await userEvent.click(screen.getByText('Anyone'))
    expect(onChange).toHaveBeenLastCalledWith(null)
  })

  it('labels the active value as "Me" for the current user', () => {
    renderChip({ value: ME })
    expect(screen.getByTestId('filter-created-by')).toHaveTextContent('Me')
  })

  it('labels the active value with the creator name', () => {
    renderChip({ value: 2 })
    expect(screen.getByTestId('filter-created-by')).toHaveTextContent('Ana Ruiz')
  })

  // Covers both a hand-typed URL and the window before the option source
  // loads. Reading "All" while a filter is active would be a lie.
  it('falls back to #id when the filtered creator is not in the options', () => {
    renderChip({ value: 99, options: [] })
    expect(screen.getByTestId('filter-created-by')).toHaveTextContent('#99')
  })
})
