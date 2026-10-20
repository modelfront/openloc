import { describe, it, expect } from 'vitest'
import { act, render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { openChat } from '../../lib/chat-bus'
import { ChatDemoPanel } from './ChatDemoPanel'

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><ChatDemoPanel /></MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ChatDemoPanel', () => {
  it('shows an example conversation, never a live chat', () => {
    renderPanel()
    fireEvent.click(screen.getByTestId('chat-launcher'))
    const messages = screen.getByTestId('chat-messages')
    expect(messages).toHaveTextContent('Create a project for summer-booking.json')
    expect(messages).toHaveTextContent('Build a shared Operations dashboard')
    expect(messages).toHaveTextContent('Needs your confirmation')
    expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('read-only')
    expect(screen.queryByTestId('chat-input')).not.toBeInTheDocument()
    expect(screen.getByText(/This is an example/)).toBeInTheDocument()
  })

  it('gives every example exchange its own tour anchor', () => {
    const { container } = renderPanel()
    fireEvent.click(screen.getByTestId('chat-launcher'))
    for (const anchor of ['chat.example.project', 'chat.example.knowledge', 'chat.example.dashboard', 'chat.example.tour', 'chat.example.confirm']) {
      expect(container.querySelector(`[data-guide="${anchor}"]`), anchor).not.toBeNull()
    }
    expect(container.querySelector('[data-guide="chat.example.dashboard"]')).toHaveTextContent('Operations')
  })

  it('opens when a guided tour points inside it', () => {
    renderPanel()
    expect(screen.queryByTestId('chat-demo-panel')).not.toBeInTheDocument()
    act(() => openChat())
    expect(screen.getByTestId('chat-demo-panel')).toBeInTheDocument()
  })
})
