import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TemplatePickerDialog } from './TemplatePickerDialog'
import { listTemplatesPage } from '../../api'
import type { Template } from '../../api'

vi.mock('../../api', () => ({ listTemplatesPage: vi.fn() }))
const listPage = vi.mocked(listTemplatesPage)

const template = (id: number, name: string, workflow_name: string | null = null): Template => ({
  id, name, workflow_name,
  mt_on_upload: true, tm_on_upload: true, fuzzy_threshold: 0.75,
  created_at: '2026-09-01T10:00:00Z',
} as Template)

const page = (data: Template[], hasMore = false) => ({
  data,
  cursor: hasMore && data.length ? data[data.length - 1].id : 0,
  has_more: hasMore,
})

// The list's sentinel drives pagination, and jsdom's stub never fires. Keep
// every observer so a test can put the sentinel "in view" itself.
const observers: { cb: IntersectionObserverCallback }[] = []
beforeEach(() => {
  observers.length = 0
  listPage.mockReset()
  listPage.mockResolvedValue(page([template(1, 'Marketing', 'Standard'), template(2, 'Legal')]))
  vi.stubGlobal('IntersectionObserver', class {
    constructor(cb: IntersectionObserverCallback) { observers.push({ cb }) }
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() { return [] }
  })
})
afterEach(() => vi.unstubAllGlobals())

const scrollToBottom = () => {
  for (const o of observers) o.cb([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
}

function renderDialog(props: Partial<Parameters<typeof TemplatePickerDialog>[0]> = {}) {
  const onPick = vi.fn()
  const onOpenChange = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <TemplatePickerDialog open onOpenChange={onOpenChange} onPick={onPick} {...props} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onPick, onOpenChange }
}

describe('TemplatePickerDialog', () => {
  it('lists every template with its workflow origin', async () => {
    renderDialog()
    expect(await screen.findByTestId('template-option-1')).toHaveTextContent('Marketing')
    expect(screen.getByRole('dialog', { name: 'Choose a template' })).toBeInTheDocument()
    expect(screen.getByTestId('template-option-1')).toHaveTextContent('Standard workflow')
    expect(screen.getByTestId('template-option-2')).toHaveTextContent('Custom workflow')
  })

  it('reports the picked template id as a string', async () => {
    const { onPick } = renderDialog()
    await userEvent.click(await screen.findByTestId('template-option-2'))
    expect(onPick).toHaveBeenCalledWith('2')
  })

  it('marks the template the form is already based on', async () => {
    renderDialog({ selectedId: '1' })
    expect(await screen.findByTestId('template-option-1')).toHaveAttribute('aria-current', 'true')
    expect(screen.getByTestId('template-option-2')).not.toHaveAttribute('aria-current')
  })

  it('points at the Templates page when there are none', async () => {
    listPage.mockResolvedValue(page([]))
    const { onPick } = renderDialog()
    expect(await screen.findByTestId('template-picker-empty')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Templates' })).toHaveAttribute('href', '/templates')
    expect(screen.queryAllByTestId(/^template-option-/)).toHaveLength(0)
    expect(onPick).not.toHaveBeenCalled()
  })

  it('a failed load shows the error with a Retry, not "No templates yet"', async () => {
    listPage.mockRejectedValueOnce(new Error('server unavailable'))
    renderDialog()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the templates: server unavailable")
    expect(screen.queryByTestId('template-picker-empty')).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('template-option-1')).toHaveTextContent('Marketing')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('searches on the server and says so when nothing matches', async () => {
    renderDialog()
    await screen.findByTestId('template-option-1')

    await userEvent.type(screen.getByTestId('template-picker-search'), 'legal')
    listPage.mockResolvedValue(page([]))

    await waitFor(() => expect(listPage).toHaveBeenCalledWith('legal', 0, expect.any(Number)))
    expect(await screen.findByTestId('template-picker-empty')).toHaveTextContent('No templates match “legal”')
  })

  it('loads the next page when the bottom of the list comes into view', async () => {
    listPage.mockResolvedValueOnce(page([template(3, 'Marketing')], true))
    renderDialog()
    await screen.findByTestId('template-option-3')

    listPage.mockResolvedValueOnce(page([template(2, 'Legal')]))
    scrollToBottom()

    expect(await screen.findByTestId('template-option-2')).toBeInTheDocument()
    // The first page stays on screen — pages accumulate, not replace.
    expect(screen.getByTestId('template-option-3')).toBeInTheDocument()
    expect(listPage).toHaveBeenLastCalledWith('', 3, expect.any(Number))
  })

  it('stops paging once the last page arrives', async () => {
    renderDialog()
    await screen.findByTestId('template-option-1')
    listPage.mockClear()

    scrollToBottom()

    await new Promise(r => setTimeout(r, 20))
    expect(listPage).not.toHaveBeenCalled()
  })

  it('closes through onOpenChange when dismissed', async () => {
    const { onOpenChange, onPick } = renderDialog()
    await screen.findByTestId('template-option-1')
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    // base-ui passes (open, eventDetails); only the flag matters here.
    expect(onOpenChange.mock.calls[0][0]).toBe(false)
    expect(onPick).not.toHaveBeenCalled()
  })
})
