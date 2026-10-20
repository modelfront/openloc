import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { CopyLinkButton } from './copy-link-button'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function stubClipboard(writeText: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
}

describe('CopyLinkButton', () => {
  afterEach(() => vi.clearAllMocks())

  it('copies the value and toasts success', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    stubClipboard(writeText)
    render(<CopyLinkButton url="https://example.com/x" />)
    fireEvent.click(screen.getByRole('button', { name: 'Copy link' }))
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Link copied'))
    expect(writeText).toHaveBeenCalledWith('https://example.com/x')
    expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })

  it('points at the address bar when copying a link fails', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    render(<CopyLinkButton url="https://example.com/x" />)
    fireEvent.click(screen.getByRole('button', { name: 'Copy link' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Could not copy the link — copy it from the address bar instead'),
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('uses errorMessage for the failure toast when given', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    render(<CopyLinkButton url="curl …" label="Copy" iconOnly errorMessage="Could not copy the code" />)
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Could not copy the code'))
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument()
  })
})
