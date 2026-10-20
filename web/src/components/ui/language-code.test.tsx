import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LanguageCode, LanguagePair } from './language-code'

describe('language code display', () => {
  it('uses monospace uppercase text without a badge background', () => {
    render(<LanguageCode code="en-US" data-testid="code" />)
    const code = screen.getByTestId('code')
    expect(code).toHaveTextContent('en-US')
    expect(code).toHaveClass('font-mono', 'text-xs', 'uppercase')
    expect(code.className).not.toMatch(/\bbg-/)
  })

  it('renders a source and multiple targets consistently', () => {
    render(<LanguagePair source="en" targets={['fr', 'de']} />)
    expect(screen.getByText('en → fr, de')).toHaveClass('font-mono', 'text-xs', 'uppercase')
  })

  it('shows every target when max is omitted', () => {
    render(<LanguagePair source="en" targets={['fr', 'de', 'es']} data-testid="pair" />)
    expect(screen.getByTestId('pair')).toHaveTextContent('en → fr, de, es')
    expect(screen.queryByText(/^\+/)).toBeNull()
  })

  it('collapses targets past max into a chip naming them', () => {
    render(<LanguagePair source="en" targets={['fr', 'de', 'es', 'it']} max={2} data-testid="pair" />)
    expect(screen.getByTestId('pair')).toHaveTextContent('en → fr, de')
    // The count is what is HIDDEN, not the total — an off-by-one here would
    // read as "+4 more" on a four-language TM that shows two.
    expect(screen.getByText('+2')).toHaveAttribute('title', 'es, it')
  })

  it('renders no chip when the target count is exactly max', () => {
    render(<LanguagePair source="en" targets={['fr', 'de']} max={2} data-testid="pair" />)
    expect(screen.getByTestId('pair')).toHaveTextContent('en → fr, de')
    expect(screen.queryByText(/^\+/)).toBeNull()
  })
})

describe('LanguagePair separator', () => {
  it('joins source and targets with the given separator', () => {
    render(<LanguagePair source="en" targets={['de', 'fr']} separator="|" data-testid="pair" />)
    expect(screen.getByTestId('pair').textContent).toBe('en | de, fr')
  })

  it('shows only the source when there are no targets', () => {
    render(<LanguagePair source="en" targets={[]} data-testid="pair" />)
    expect(screen.getByTestId('pair').textContent).toBe('en')
  })
})
