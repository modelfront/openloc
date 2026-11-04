import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const createInvite = vi.fn()
vi.mock('../../api', () => ({
  createInvite: (...args: unknown[]) => createInvite(...args),
}))

import { InviteDialog } from './InviteDialog'

const onInvited = vi.fn()

async function openVendorDialog() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <InviteDialog
        title="Invite vendor"
        description="Creates an organization for the vendor."
        icon={null}
        roles={['admin']}
        emailLabel="Admin email"
        orgNameField={{ label: 'Vendor name', placeholder: 'Acme Translations' }}
        placeholder="admin@vendor.example.com"
        testIdPrefix="vendor-invite"
        guide="org.vendor_invite"
        onInvited={onInvited}
      />
    </QueryClientProvider>,
  )
  await userEvent.setup().click(screen.getByTestId('vendor-invite-open'))
  await screen.findByTestId('vendor-invite-dialog')
  // The popup puts the cursor in its first field.
  await waitFor(() => expect(screen.getByTestId('vendor-invite-org-name')).toHaveFocus())
}

const submit = () => screen.getByTestId('vendor-invite-submit')

beforeEach(() => {
  vi.clearAllMocks()
  createInvite.mockResolvedValue({})
})

describe('InviteDialog with an org name field', () => {
  it('opens from its button and labels the fields Vendor name and Admin email', async () => {
    await openVendorDialog()
    expect(screen.getByText('Vendor name')).toBeInTheDocument()
    expect(screen.getByText('Admin email')).toBeInTheDocument()
    expect(screen.getByTestId('vendor-invite-org-name')).toHaveAttribute('maxlength', '120')
  })

  it('keeps Send invite disabled until both the name and the email are filled', async () => {
    const user = userEvent.setup()
    await openVendorDialog()
    await user.type(screen.getByTestId('vendor-invite-email'), 'boss@vendor.com')
    await waitFor(() => expect(submit()).toHaveAttribute('aria-disabled', 'true'))
    await user.hover(submit())
    expect(await screen.findByText('Fill in the required fields first.')).toBeInTheDocument()

    // A name of only spaces is no name.
    await user.type(screen.getByTestId('vendor-invite-org-name'), '   ')
    await waitFor(() => expect(submit()).toHaveAttribute('aria-disabled', 'true'))

    await user.type(screen.getByTestId('vendor-invite-org-name'), 'Acme Translations')
    await waitFor(() => expect(submit()).not.toHaveAttribute('aria-disabled'))
    expect(createInvite).not.toHaveBeenCalled()
  })

  it('sends the trimmed name with the admin role, then closes', async () => {
    const user = userEvent.setup()
    await openVendorDialog()
    await user.type(screen.getByTestId('vendor-invite-org-name'), '  Acme Translations ')
    await user.type(screen.getByTestId('vendor-invite-email'), ' boss@vendor.com ')
    await user.click(submit())
    await waitFor(() => expect(createInvite).toHaveBeenCalledWith('boss@vendor.com', 'admin', 'Acme Translations'))
    await waitFor(() => expect(screen.queryByTestId('vendor-invite-dialog')).toBeNull())
    expect(onInvited).toHaveBeenCalledTimes(1)
  })

  it('Cancel closes the popup and drops what was typed', async () => {
    const user = userEvent.setup()
    await openVendorDialog()
    await user.type(screen.getByTestId('vendor-invite-org-name'), 'Half typed')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('vendor-invite-dialog')).toBeNull())
    await user.click(screen.getByTestId('vendor-invite-open'))
    expect(await screen.findByTestId('vendor-invite-org-name')).toHaveValue('')
    expect(createInvite).not.toHaveBeenCalled()
  })
})

describe('InviteDialog without an org name field', () => {
  it('has no name field and sends no org name', async () => {
    const user = userEvent.setup()
    const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <InviteDialog
          title="Invite member"
          description="They get an email with a link to join this organization."
          icon={null}
          roles={['linguist']}
          placeholder="colleague@example.com"
          testIdPrefix="member-invite"
          guide="org.invite"
          onInvited={onInvited}
        />
      </QueryClientProvider>,
    )
    await user.click(screen.getByTestId('member-invite-open'))
    await screen.findByTestId('member-invite-dialog')
    await waitFor(() => expect(screen.getByTestId('member-invite-email')).toHaveFocus())
    expect(screen.queryByTestId('member-invite-org-name')).toBeNull()
    await user.type(screen.getByTestId('member-invite-email'), 'new@example.com')
    await user.click(screen.getByTestId('member-invite-submit'))
    await waitFor(() => expect(createInvite).toHaveBeenCalledWith('new@example.com', 'linguist', undefined))
  })
})

describe('InviteDialog errors', () => {
  async function openMemberDialog() {
    const user = userEvent.setup()
    const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <InviteDialog
          title="Invite member"
          description="They get an email with a link to join this organization."
          icon={null}
          roles={['linguist']}
          placeholder="colleague@example.com"
          testIdPrefix="member-invite"
          guide="org.invite"
          onInvited={onInvited}
        />
      </QueryClientProvider>,
    )
    await user.click(screen.getByTestId('member-invite-open'))
    await waitFor(() => expect(screen.getByTestId('member-invite-email')).toHaveFocus())
    return user
  }

  it('shows no error while the popup is being closed', async () => {
    const user = await openMemberDialog()
    await user.type(screen.getByTestId('member-invite-email'), 'not-an-email')
    // Pressing Cancel takes the focus out of the field before the click closes the popup.
    await user.pointer({ keys: '[MouseLeft>]', target: screen.getByRole('button', { name: 'Cancel' }) })
    expect(screen.queryByText('Valid email is required')).toBeNull()
  })

  it('does not send a bad email, and Send says why', async () => {
    const user = await openMemberDialog()
    await user.type(screen.getByTestId('member-invite-email'), 'not-an-email{Enter}')
    const send = screen.getByTestId('member-invite-submit')
    expect(send).toHaveAttribute('aria-disabled', 'true')
    await user.hover(send)
    expect(await screen.findByText('Enter a valid email address first.')).toBeInTheDocument()
    expect(createInvite).not.toHaveBeenCalled()
  })
})

