import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AppearanceSection } from './AppearanceSection'
import type { Appearance } from '../../api/appearance'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('next-themes', () => ({ useTheme: () => ({ theme: 'dark', setTheme: vi.fn() }) }))

const save = vi.fn()
let appearance: Appearance
vi.mock('../../hooks/useAppearance', () => ({
  useAppearance: () => ({ appearance, save, saving: false }),
}))

beforeEach(() => {
  save.mockClear()
  appearance = { light_palette: 'light-default', dark_palette: 'black-true', custom_palettes: [] }
})

describe('AppearanceSection', () => {
  it('renders the built-in palettes for both modes', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('True black')).toBeInTheDocument()
    expect(screen.getByText('Graphite')).toBeInTheDocument()
    expect(screen.getByText('Dim')).toBeInTheDocument()
    expect(screen.getByText('Default light')).toBeInTheDocument()
  })

  it('saves the chosen palette for its mode', async () => {
    const user = userEvent.setup()
    render(<AppearanceSection />)
    await user.click(screen.getByTestId('palette-graphite'))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ dark_palette: 'graphite' }))
  })

  it('clones a built-in into an editable custom copy (pre-filled from the source)', async () => {
    const user = userEvent.setup()
    render(<AppearanceSection />)
    await user.click(screen.getByLabelText('Clone Dim'))
    const dialog = await screen.findByRole('dialog')
    // Pre-filled from Dim: "(copy)" name + Dim's colors.
    expect(within(dialog).getByPlaceholderText('My palette')).toHaveValue('Dim (copy)')
    await user.click(within(dialog).getByRole('button', { name: /^create$/i }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      dark_palette: expect.stringMatching(/^custom-/), // new id, not 'dim'
      custom_palettes: expect.arrayContaining([
        expect.objectContaining({
          name: 'Dim (copy)',
          mode: 'dark',
          anchors: expect.objectContaining({ bg: '#09090b', accent: '#3b82f6' }),
        }),
      ]),
    }))
  })

  it('creates a custom palette via the editor and makes it active', async () => {
    const user = userEvent.setup()
    render(<AppearanceSection />)
    // First "New palette" button belongs to the Dark group (rendered first).
    await user.click(screen.getAllByText('New palette')[0])
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByPlaceholderText('My palette'), 'Midnight')
    await user.click(within(dialog).getByRole('button', { name: /^create$/i }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      dark_palette: expect.stringMatching(/^custom-/),
      custom_palettes: expect.arrayContaining([
        expect.objectContaining({ name: 'Midnight', mode: 'dark' }),
      ]),
    }))
  })
})
