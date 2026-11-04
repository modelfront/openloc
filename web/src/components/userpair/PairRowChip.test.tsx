import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PairRowChip } from './LanguagePairsEditor'

const TARGETS = ['de', 'es', 'fr', 'it', 'ja', 'nl', 'pl', 'pt-br', 'sv', 'zh-cn']

describe('PairRowChip', () => {
  it('shows a short row in full, with no count', () => {
    render(<PairRowChip row={{ sources: ['en', 'fr'], targets: ['de', 'es'] }} />)
    const chip = screen.getByTestId('pair-row-chip')
    expect(chip).toHaveTextContent(/^en, fr\s*→\s*de, es$/)
    expect(chip).not.toHaveTextContent('+')
  })

  it('lists two languages a side, then +N, for sources and targets', () => {
    render(<PairRowChip row={{ sources: ['en', 'de', 'fr'], targets: TARGETS }} />)
    expect(screen.getByTestId('pair-row-chip')).toHaveTextContent(/^en, de\s*\+1\s*→\s*de, es\s*\+8$/)
  })

  it('gives the full list in a tooltip when languages are hidden', async () => {
    const user = userEvent.setup()
    render(<PairRowChip row={{ sources: ['en'], targets: TARGETS }} />)
    await user.hover(screen.getByTestId('pair-row-chip'))
    expect(await screen.findByText('en → de, es, fr, it, ja, nl, pl, pt-br, sv, zh-cn')).toBeInTheDocument()
  })
})
