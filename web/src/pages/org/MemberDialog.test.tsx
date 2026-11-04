import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return {
    ...actual,
    listLanguages: vi.fn(),
    listUserLanguagePairs: vi.fn(),
    replaceUserLanguagePairs: vi.fn(),
    updateMemberRole: vi.fn(),
  }
})

import { toast } from 'sonner'
import { listLanguages, listUserLanguagePairs, replaceUserLanguagePairs, updateMemberRole, type Member } from '../../api'
import MemberDialog from './MemberDialog'
import { memberRoles } from '../../lib/roles'

const dana: Member = { id: 7, name: 'Dana', email: 'dana@example.com', role: 'project_manager', created_at: '2026-10-01T00:00:00Z' }
const twoRows = [
  { source_lang: 'en', target_lang: 'de', row_group: 0 },
  { source_lang: 'fr', target_lang: 'de', row_group: 1 },
]

const onClose = vi.fn()
const onSaved = vi.fn()

function renderDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemberDialog member={dana} roles={memberRoles(false)} onClose={onClose} onSaved={onSaved} />
    </QueryClientProvider>,
  )
}

const saveButton = () => screen.getByTestId('member-save')
const roleSelect = () => screen.getByTestId('member-role')

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listLanguages).mockResolvedValue([
    { code: 'en', name: 'English' }, { code: 'de', name: 'German' }, { code: 'fr', name: 'French' },
  ] as Awaited<ReturnType<typeof listLanguages>>)
  vi.mocked(listUserLanguagePairs).mockResolvedValue(twoRows)
  vi.mocked(replaceUserLanguagePairs).mockImplementation(async (_id, pairs) => pairs)
  vi.mocked(updateMemberRole).mockResolvedValue({ status: 'ok', role: 'admin' })
})

describe('MemberDialog', () => {
  it('keeps Save disabled until the role or the pairs change', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    expect(saveButton()).toHaveAttribute('aria-disabled', 'true')
    await user.click(saveButton())
    expect(updateMemberRole).not.toHaveBeenCalled()
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()

    await user.selectOptions(roleSelect(), 'admin')
    expect(saveButton()).not.toHaveAttribute('aria-disabled')
    await user.selectOptions(roleSelect(), 'project_manager')
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true')
  })

  it('saves a role change alone without touching the pairs', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    await user.selectOptions(roleSelect(), 'admin')
    await user.click(saveButton())

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(updateMemberRole).toHaveBeenCalledWith(7, 'admin')
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()
    expect(onSaved).toHaveBeenCalledWith({ role: 'admin' })
    expect(toast.success).toHaveBeenCalledWith('Dana is now Admin.')
  })

  it('saves the role and the pairs together with one Save', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    await user.selectOptions(roleSelect(), 'admin')
    await user.click(screen.getByRole('button', { name: 'Remove row 2' }))
    await user.click(saveButton())

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(updateMemberRole).toHaveBeenCalledWith(7, 'admin')
    expect(replaceUserLanguagePairs).toHaveBeenCalledWith(7, [{ source_lang: 'en', target_lang: 'de', row_group: 0 }])
    expect(onSaved).toHaveBeenCalledWith({ role: 'admin' })
    expect(onSaved).toHaveBeenCalledWith({ pairs: [{ source_lang: 'en', target_lang: 'de', row_group: 0 }] })
    expect(toast.success).toHaveBeenCalledWith('Dana is now Admin. Language pairs saved.')
  })

  it('reports a saved role and stays open when the pairs then fail', async () => {
    vi.mocked(replaceUserLanguagePairs).mockRejectedValue(new Error('server unavailable'))
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    await user.selectOptions(roleSelect(), 'admin')
    await user.click(screen.getByRole('button', { name: 'Remove row 2' }))
    await user.click(saveButton())

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(onSaved).toHaveBeenCalledWith({ role: 'admin' })
    expect(onSaved).toHaveBeenCalledTimes(1)
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTestId('member-dialog')).toBeInTheDocument()
  })

  it('asks before switching a member down to linguist', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    await user.selectOptions(roleSelect(), 'linguist')
    await user.click(saveButton())

    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('Change role to Linguist?')
    expect(confirm).toHaveTextContent('see only the tasks assigned to them')
    expect(updateMemberRole).not.toHaveBeenCalled()

    await user.click(within(confirm).getByRole('button', { name: 'Change to linguist' }))
    await waitFor(() => expect(updateMemberRole).toHaveBeenCalledWith(7, 'linguist'))
  })

  it('asks before removing every pair', async () => {
    const user = userEvent.setup()
    renderDialog()
    await screen.findByTestId('pair-row-1')

    await user.click(screen.getByRole('button', { name: 'Remove row 2' }))
    await user.click(screen.getByRole('button', { name: 'Remove row 1' }))
    await user.click(saveButton())

    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('Remove all language pairs for Dana?')
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()

    await user.click(within(confirm).getByRole('button', { name: 'Remove all' }))
    await waitFor(() => expect(replaceUserLanguagePairs).toHaveBeenCalledWith(7, []))
  })
})

describe('MemberDialog while the saved pairs are not loaded', () => {
  it('a failed load shows Retry, and a role change saves without erasing the pairs', async () => {
    vi.mocked(listUserLanguagePairs).mockRejectedValue(new Error('server unavailable'))
    const user = userEvent.setup()
    renderDialog()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the language pairs: server unavailable")
    expect(within(alert).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.queryByTestId('pair-row-0')).not.toBeInTheDocument()

    await user.selectOptions(roleSelect(), 'admin')
    await user.click(saveButton())
    await waitFor(() => expect(updateMemberRole).toHaveBeenCalledWith(7, 'admin'))
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()
  })

  it('Retry that succeeds shows the saved pairs', async () => {
    vi.mocked(listUserLanguagePairs)
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValue(twoRows)
    const user = userEvent.setup()
    renderDialog()

    await user.click(await screen.findByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('2 pairs:')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a role change while the pairs are loading saves only the role', async () => {
    vi.mocked(listUserLanguagePairs).mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    renderDialog()

    await screen.findByText('Loading…')
    await user.selectOptions(roleSelect(), 'admin')
    await user.click(saveButton())

    await waitFor(() => expect(updateMemberRole).toHaveBeenCalledWith(7, 'admin'))
    expect(replaceUserLanguagePairs).not.toHaveBeenCalled()
  })
})
