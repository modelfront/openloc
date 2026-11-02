import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConnectionForm } from './ConnectionForm'
import type { AIConnection } from '../../api'

const saved: AIConnection = {
  id: 1, provider: 'openai', name: 'OpenAI', base_url: 'https://acme.openai.azure.com',
  token_count: 1, created_at: '2026-08-01T00:00:00Z',
}

// Re-query each time: the button is remounted when its disabled reason comes or goes.
const update = () => screen.getByTestId('ai-connection-save')

describe('ConnectionForm Update', () => {
  it('is disabled until the endpoint changes, then saves it', async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    render(<ConnectionForm provider="openai" initial={saved} onSubmit={onSubmit} isPending={false} />)

    expect(update()).toHaveAttribute('aria-disabled', 'true')
    await user.click(update())
    expect(onSubmit).not.toHaveBeenCalled()

    const field = screen.getByTestId('ai-connection-base-url')
    await user.clear(field)
    await user.type(field, 'https://example.com')
    expect(update()).not.toHaveAttribute('aria-disabled')
    await user.click(update())
    expect(onSubmit).toHaveBeenCalledWith({ base_url: 'https://example.com' })
  })

  it('is clean again once the saved endpoint comes back', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<ConnectionForm provider="openai" initial={saved} onSubmit={vi.fn()} isPending={false} />)
    const field = screen.getByTestId('ai-connection-base-url')
    await user.clear(field)
    await user.type(field, 'https://example.com')
    rerender(<ConnectionForm provider="openai" initial={{ ...saved, base_url: 'https://example.com' }} onSubmit={vi.fn()} isPending={false} />)
    expect(update()).toHaveAttribute('aria-disabled', 'true')
  })

  it('is enabled for a provider with no connection yet', () => {
    render(<ConnectionForm provider="openai" onSubmit={vi.fn()} isPending={false} />)
    expect(update()).toBeEnabled()
    expect(update()).not.toHaveAttribute('aria-disabled')
  })
})
