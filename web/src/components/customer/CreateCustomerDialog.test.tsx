import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateCustomerDialog } from './CreateCustomerDialog'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
const createCustomer = vi.fn()
vi.mock('../../api', () => ({ createCustomer: (name: string) => createCustomer(name) }))

describe('CreateCustomerDialog', () => {
  it('keeps Create disabled until a name is typed, then creates with the trimmed name', async () => {
    createCustomer.mockResolvedValue({ id: 7, name: 'Acme Corp' })
    const onCreated = vi.fn()
    const user = userEvent.setup()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <CreateCustomerDialog open onClose={vi.fn()} onCreated={onCreated} />
      </QueryClientProvider>,
    )
    const create = () => screen.getByRole('button', { name: 'Create' })
    await waitFor(() => expect(create()).toHaveAttribute('aria-disabled', 'true'))
    await user.hover(create())
    expect(await screen.findByText('Fill in the required fields first.')).toBeInTheDocument()
    await user.click(create())
    expect(createCustomer).not.toHaveBeenCalled()

    await user.type(screen.getByPlaceholderText('Customer name (e.g. Acme Corp)'), '  Acme Corp ')
    await waitFor(() => expect(create()).not.toHaveAttribute('aria-disabled'))
    await user.click(create())
    await waitFor(() => expect(createCustomer).toHaveBeenCalledWith('Acme Corp'))
  })
})
