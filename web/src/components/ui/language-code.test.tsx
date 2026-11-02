import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { LanguageCode, LanguagePair } from './language-code'
import { TooltipProvider } from './tooltip'

describe('language code display', () => {
  it('uses monospace uppercase text without a badge background', () => {
    render(<LanguageCode code="en-US" data-testid="code" />)
    const code = screen.getByTestId('code')
    expect(code).toHaveTextContent('en-US')
    expect(code).toHaveClass('font-mono', 'text-xs', 'uppercase')
    expect(code.className).not.toMatch(/\bbg-/)
  })

  it('renders a source and multiple targets consistently', () => {
    render(<LanguagePair source="en" targets={['fr', 'de']} data-testid="pair" />)
    const pair = screen.getByTestId('pair')
    expect(pair.textContent).toBe('en → fr, de')
    expect(pair).toHaveClass('font-mono', 'text-xs', 'uppercase')
  })

  it('shows every target when max is omitted', () => {
    render(<LanguagePair source="en" targets={['fr', 'de', 'es']} data-testid="pair" />)
    expect(screen.getByTestId('pair')).toHaveTextContent('en → fr, de, es')
    expect(screen.queryByText(/^\+/)).toBeNull()
  })

  it('collapses targets past max into a chip whose tooltip shows the full list, styled like the pair', async () => {
    const user = userEvent.setup()
    // The app's provider opens at once by default; Base UI alone waits 600 ms.
    render(
      <TooltipProvider>
        <LanguagePair source="en" targets={['fr', 'de', 'es', 'it']} separator="|" max={2} data-testid="pair" />
      </TooltipProvider>,
    )
    expect(screen.getByTestId('pair')).toHaveTextContent('en | fr, de')
    // The count is what is HIDDEN, not the total — an off-by-one here would
    // read as "+4 more" on a four-language TM that shows two.
    const chip = screen.getByText('+2')
    // No native title: the browser would show a second, unstyled tooltip.
    expect(chip).not.toHaveAttribute('title')

    await user.hover(chip)
    await waitFor(() => {
      expect(document.querySelector('[data-slot="tooltip-content"]')).not.toBeNull()
    })
    const list = document.querySelector('[data-slot="tooltip-content"] .font-mono')
    expect(list?.textContent).toBe('en | fr, de, es, it')
    expect(list).toHaveClass('font-mono', 'text-xs', 'uppercase')
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
