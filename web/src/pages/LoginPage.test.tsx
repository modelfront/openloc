import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'

const devLogin = vi.fn()
vi.mock('../api', () => ({
  getAuthConfig: () => Promise.resolve({ google_enabled: false, microsoft_enabled: false, dev_login_enabled: true }),
  devLogin: (who?: unknown) => devLogin(who),
  passwordLogin: vi.fn(),
  saveLoginResult: vi.fn(),
}))

import LoginPage from './LoginPage'

// ?signedout=1 keeps dev mode from logging in by itself on load.
function renderPage() {
  return render(<MemoryRouter initialEntries={['/login?signedout=1']}><LoginPage /></MemoryRouter>)
}

beforeEach(() => {
  devLogin.mockReset()
  devLogin.mockResolvedValue({ token: 't', needs_org_selection: false, user: {} })
})

describe('LoginPage dev buttons', () => {
  it('Vendor login signs in as the seed vendor', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Vendor login' }))
    await waitFor(() => expect(devLogin).toHaveBeenCalledWith({ as: 'vendor' }))
  })

  it('Dev Login signs in as the default seed owner', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Dev Login' }))
    await waitFor(() => expect(devLogin).toHaveBeenCalledWith(undefined))
  })
})
