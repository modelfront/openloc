import { describe, it, expect } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { Toaster } from './sonner'

describe('Toaster', () => {
  it('gives every toast a close button that removes it at once', async () => {
    render(<Toaster />)
    act(() => { toast.success('Uploaded file.xliff') })
    expect(await screen.findByText('Uploaded file.xliff')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Close toast' }))
    await waitFor(() => expect(screen.queryByText('Uploaded file.xliff')).not.toBeInTheDocument())
  })
})
