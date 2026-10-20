import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateOrgCard } from './CreateOrgCard'
// Resolves to the mocked class defined in the vi.mock factory below.
import { ApiError } from '../api'

const mockCreateOrg = vi.fn()
vi.mock('../api', () => {
  // Defined inside the factory (hoisted) so `instanceof ApiError` works in the
  // component. Re-exported for the test to construct rejections.
  class ApiError extends Error {
    status: number
    body?: unknown
    constructor(status: number, message: string, body?: unknown) {
      super(message)
      this.status = status
      this.body = body
      this.name = 'ApiError'
    }
  }
  return {
    createOrg: (...args: unknown[]) => mockCreateOrg(...args),
    ApiError,
  }
})

// jsdom has no navigation; stub window.location so the success redirect is inert.
beforeEach(() => {
  mockCreateOrg.mockReset()
  Object.defineProperty(window, 'location', { value: { href: '' }, writable: true })
})

function renderCard(email?: string) {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <CreateOrgCard email={email} />
    </QueryClientProvider>,
  )
}

describe('CreateOrgCard', () => {
  it('offers org creation for a work email, prefilled from the domain', async () => {
    renderCard('john@nordwind.example')
    // Confirmation copy names the domain.
    expect(screen.getByText('nordwind.example')).toBeInTheDocument()
    const input = screen.getByPlaceholderText('Nordwind') as HTMLInputElement
    expect(input).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create organization/i })).toBeInTheDocument()
  })

  it('creates the org and redirects to awaiting-approval', async () => {
    mockCreateOrg.mockResolvedValue({ token: 't', status: 'pending', tenant: {} })
    const user = userEvent.setup()
    renderCard('owner@acmecorp.com')

    await user.click(screen.getByRole('button', { name: /create organization/i }))
    await waitFor(() => expect(mockCreateOrg).toHaveBeenCalled())
    await waitFor(() => expect(window.location.href).toBe('/awaiting-approval'))
  })

  it('blocks personal email providers with guidance, no create button', () => {
    renderCard('someone@gmail.com')
    expect(screen.getByText(/personal email accounts can.t create/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /create organization/i })).not.toBeInTheDocument()
  })

  it('shows a friendly message when the backend rejects a free email', async () => {
    // Backend is authoritative: even if the client check misses, the coded
    // error is surfaced.
    mockCreateOrg.mockRejectedValue(new ApiError(400, 'nope', { error: { code: 'free_email' } }))
    const user = userEvent.setup()
    renderCard('weird@corp-that-looks-real.com')

    await user.click(screen.getByRole('button', { name: /create organization/i }))
    await waitFor(() =>
      expect(screen.getByText(/personal email addresses can.t create an organization/i)).toBeInTheDocument(),
    )
  })
})
