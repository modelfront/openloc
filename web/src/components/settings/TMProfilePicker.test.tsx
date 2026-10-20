import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ComponentProps, ReactNode } from 'react'
import type { ComboboxProps } from '@/components/ui/combobox'
import type ConfirmDialog from '../ConfirmDialog'
import type { TMProfileNameDialog } from '../tm/TMProfileNameDialog'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TMProfilePicker } from './TMProfilePicker'

vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, actions, 'data-testid': testId }: ComboboxProps) => (
    <>
      {/* data-value carries the raw prop: a <select> whose value matches no
          option falls back to the first one, which the real Combobox does not
          (it shows its placeholder). */}
      <select data-testid={testId} data-value={value} value={value} onChange={e => onChange(e.target.value)}>
        {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      {(actions ?? []).map(a => (
        <button key={a.key} data-testid={`${testId}-action-${a.key}`} onClick={a.onSelect}>{a.label}</button>
      ))}
    </>
  ),
}))
vi.mock('@/components/ui/tooltip', () => ({
  Tooltip: ({ children }: { children?: ReactNode }) => children,
  TooltipTrigger: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
  TooltipContent: ({ children }: { children?: ReactNode }) => children,
  TooltipProvider: ({ children }: { children?: ReactNode }) => children,
}))
// ConfirmDialog → visible buttons so the detach confirmation can be driven.
vi.mock('../ConfirmDialog', () => ({
  default: ({ open, onConfirm, onOpenChange, title, description }: ComponentProps<typeof ConfirmDialog>) => open ? (
    <div data-testid="confirm-dialog">
      <span>{title}</span>
      <p>{description}</p>
      <button onClick={onConfirm}>confirm</button>
      <button onClick={() => onOpenChange(false)}>cancel</button>
    </div>
  ) : null,
}))
vi.mock('../tm/TMProfileNameDialog', () => ({
  TMProfileNameDialog: ({ open, onSubmit, linkOption }: ComponentProps<typeof TMProfileNameDialog>) => open ? (
    <>
      <button data-testid="name-dialog-submit" onClick={() => onSubmit('Snap', 'desc', linkOption?.defaultChecked ?? false)}>submit</button>
      <button data-testid="name-dialog-submit-nolink" onClick={() => onSubmit('Snap', 'desc', false)}>submit without link</button>
    </>
  ) : null,
}))

const profiles = [{ id: 7, name: 'Acme legal' }, { id: 9, name: 'Marketing' }]

function renderPicker(props: Partial<Parameters<typeof TMProfilePicker>[0]> = {}) {
  const defaults = {
    profiles, value: null as number | null, isCreate: false,
    onLink: vi.fn(), onDetach: vi.fn(), onClearDraft: vi.fn(), onSaveAsProfile: vi.fn().mockResolvedValue(undefined),
    ...props,
  }
  render(<MemoryRouter><TMProfilePicker {...defaults} /></MemoryRouter>)
  return defaults
}

beforeEach(() => vi.clearAllMocks())

describe('TMProfilePicker', () => {
  const select = () => screen.getByTestId('tm-profile-select') as HTMLSelectElement
  const labels = () => Array.from(select().options).map(o => o.textContent)

  it('one dropdown: the own-TMs entry is first, then the profiles', () => {
    renderPicker()
    expect(select().value).toBe('custom')
    expect(labels()).toEqual(['From scratch', 'Acme legal', 'Marketing'])
    expect(screen.queryByTestId('tm-profile-linked')).toBeNull()
  })

  it('with no profiles the dropdown still offers the own setup + save-as, and points at the TM page', () => {
    renderPicker({ profiles: [] })
    expect(labels()).toEqual(['From scratch'])
    expect(screen.getByTestId('tm-profile-save-as')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /tm profiles/i })).toHaveAttribute('href', '/memories/profiles')
  })

  it('nothing chosen yet: the dropdown is empty and nothing about the own setup is offered', () => {
    renderPicker({ value: undefined })
    expect(select()).toHaveAttribute('data-value', '')
    expect(screen.queryByTestId('tm-profile-save-as')).toBeNull()
    expect(screen.queryByTestId('tm-profile-linked')).toBeNull()
  })

  it('choosing "From scratch" from nothing settles the choice without a confirmation', () => {
    const onChooseOwn = vi.fn()
    const p = renderPicker({ value: undefined, onChooseOwn })
    fireEvent.change(select(), { target: { value: 'custom' } })
    expect(onChooseOwn).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('confirm-dialog')).toBeNull()
    expect(p.onDetach).not.toHaveBeenCalled()
    expect(p.onClearDraft).not.toHaveBeenCalled()
  })

  it('a profile can be picked straight from nothing', () => {
    const p = renderPicker({ value: undefined })
    fireEvent.change(select(), { target: { value: '7' } })
    expect(p.onLink).toHaveBeenCalledWith(7)
  })

  // `entity` only changes copy; the detach confirmation is where it shows.
  it.each([
    ['project', undefined],
    ['template', 'template' as const],
  ])('detach wording names the %s', (word, entity) => {
    renderPicker({ value: 7, entity })
    fireEvent.change(select(), { target: { value: 'custom' } })
    expect(screen.getByTestId('confirm-dialog')).toHaveTextContent(`copied into this ${word}`)
  })

  it('picking a profile links it', () => {
    const p = renderPicker()
    fireEvent.change(select(), { target: { value: '9' } })
    expect(p.onLink).toHaveBeenCalledWith(9)
  })

  it('while linked: the dropdown shows the profile, the status line names it with a link, and Detach is offered', () => {
    renderPicker({ value: 7 })
    expect(select().value).toBe('7')
    expect(screen.getByTestId('tm-profile-linked')).toHaveTextContent('Acme legal')
    expect(screen.getByRole('link', { name: /Acme legal/ })).toHaveAttribute('href', '/memories/profiles/7')
    expect(screen.getByTestId('tm-profile-detach')).toBeInTheDocument()
    expect(screen.queryByTestId('tm-profile-save-as')).toBeNull()
  })

  it('edit mode: choosing the own-TMs entry while linked asks first, then detaches', () => {
    const p = renderPicker({ value: 7 })
    fireEvent.change(select(), { target: { value: 'custom' } })
    expect(p.onDetach).not.toHaveBeenCalled()
    expect(screen.getByTestId('confirm-dialog')).toHaveTextContent(/stop following/i)
    fireEvent.click(screen.getByText('confirm'))
    expect(p.onDetach).toHaveBeenCalledTimes(1)
  })

  it('cancelling the detach confirmation changes nothing', () => {
    const p = renderPicker({ value: 7 })
    fireEvent.click(screen.getByTestId('tm-profile-detach'))
    fireEvent.click(screen.getByText('cancel'))
    expect(p.onDetach).not.toHaveBeenCalled()
  })

  it('switching from one profile to another links directly (no copy step)', () => {
    const p = renderPicker({ value: 7 })
    fireEvent.change(select(), { target: { value: '9' } })
    expect(p.onLink).toHaveBeenCalledWith(9)
    expect(screen.queryByTestId('confirm-dialog')).toBeNull()
  })

  it('create mode: the own-TMs entry clears the draft without a confirmation, and there is no Detach', () => {
    const p = renderPicker({ value: 7, isCreate: true })
    expect(screen.queryByTestId('tm-profile-detach')).toBeNull()
    fireEvent.change(select(), { target: { value: 'custom' } })
    expect(p.onClearDraft).toHaveBeenCalledTimes(1)
    expect(p.onDetach).not.toHaveBeenCalled()
  })

  it('own TMs: "Save as TM profile" is a button beside the dropdown; follows by default, or stays own', () => {
    const p = renderPicker()
    fireEvent.click(screen.getByTestId('tm-profile-save-as'))
    fireEvent.click(screen.getByTestId('name-dialog-submit'))
    expect(p.onSaveAsProfile).toHaveBeenCalledWith('Snap', 'desc', true)
    fireEvent.click(screen.getByTestId('tm-profile-save-as'))
    fireEvent.click(screen.getByTestId('name-dialog-submit-nolink'))
    expect(p.onSaveAsProfile).toHaveBeenLastCalledWith('Snap', 'desc', false)
  })
})
