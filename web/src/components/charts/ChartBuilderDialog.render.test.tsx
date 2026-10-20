// The builder form as a user meets it: field order, the title that follows the
// metric, and the way out to the assistant.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, within, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getMetricsCatalog: vi.fn(), suggestChart: vi.fn(), createChart: vi.fn() }
})

import { getMetricsCatalog, suggestChart, createChart, type DashboardChart } from '@/api'
import { CHAT_PROMPT_EVENT, type ChatPromptDetail } from '@/lib/chat-bus'
import { metricLabel } from '@/lib/metric-labels'
import { ChartBuilderDialog } from './ChartBuilderDialog'

const CATALOG = {
  grains: ['day', 'week', 'month'],
  measures: [
    {
      name: 'words_processed', definition: 'Words.', dimensions: ['target_language'],
      values: [{ name: 'words', unit: 'words' }], filters: ['date_range'],
    },
    {
      name: 'qa_issues', definition: 'QA findings.', dimensions: ['severity'],
      values: [{ name: 'issues', unit: 'issues' }], filters: ['date_range'],
    },
  ],
}

const CHART: DashboardChart = {
  id: 11,
  title: 'Weekly volume',
  definition: { measure: 'words_processed', dimensions: ['week'], last_days: 90 },
  viz: { type: 'line', x_key: 'week', series: ['words'] },
  controls: { controls: [] },
  available_controls: [],
  position: 1,
}

function renderDialog(props: Partial<Parameters<typeof ChartBuilderDialog>[0]> = {}) {
  const onClose = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ChartBuilderDialog open onClose={onClose} dashboardId={1} dashboardName="Operations" {...props} />
    </QueryClientProvider>,
  )
  return { onClose }
}

const title = () => screen.getByTestId('chart-title') as HTMLInputElement
const pick = async (name: string) => fireEvent.click(await screen.findByTestId(`measure-${name}`))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getMetricsCatalog).mockResolvedValue(CATALOG)
  vi.mocked(createChart).mockResolvedValue({ id: 99 })
})

describe('ChartBuilderDialog', () => {
  it('lists the metrics alphabetically by the name it shows, not in the server\'s order', async () => {
    vi.mocked(getMetricsCatalog).mockResolvedValueOnce({
      ...CATALOG,
      measures: [...CATALOG.measures, {
        name: 'ai_token_usage', definition: 'Tokens.', dimensions: [],
        values: [{ name: 'total_tokens', unit: 'tokens' }], filters: ['date_range'],
      }],
    })
    renderDialog()
    const list = await screen.findByTestId('measure-list')
    await within(list).findByTestId('measure-ai_token_usage')
    expect(within(list).getAllByRole('radio').map(b => b.textContent)).toEqual(['AI token usage', 'Post-editing volume', 'QA issues'])
  })

  it('a failed catalog load shows the error with a Retry instead of loading forever', async () => {
    vi.mocked(getMetricsCatalog).mockRejectedValueOnce(new Error('server unavailable'))
    renderDialog()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the metrics: server unavailable")
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.queryByTestId('measure-list')).not.toBeInTheDocument()
    expect(screen.queryByTestId('create-chart-submit')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('measure-words_processed')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByTestId('create-chart-submit')).toBeInTheDocument()
  })

  it('asks for the title before the chart type', async () => {
    renderDialog()
    await pick('words_processed')
    const type = screen.getByLabelText('Chart type')
    expect(title().compareDocumentPosition(type) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('updates the suggested title when the metric changes', async () => {
    renderDialog()
    await pick('words_processed')
    expect(title().value).toBe(metricLabel('words_processed'))
    await pick('qa_issues')
    expect(title().value).toBe(metricLabel('qa_issues'))
  })

  it('keeps a title the user wrote', async () => {
    renderDialog()
    await pick('words_processed')
    fireEvent.change(title(), { target: { value: 'My numbers' } })
    await pick('qa_issues')
    expect(title().value).toBe('My numbers')
  })

  it("keeps an edited chart's own title when its metric changes", async () => {
    renderDialog({ chart: CHART })
    await pick('qa_issues')
    expect(title().value).toBe('Weekly volume')
  })

  // A chart saved under its metric's name still carries the suggestion.
  it("updates an edited chart's title while it is still the metric's name", async () => {
    renderDialog({ chart: { ...CHART, title: metricLabel('words_processed') } })
    await pick('qa_issues')
    expect(title().value).toBe(metricLabel('qa_issues'))
  })

  // Says which field is missing rather than only greying the button out.
  it('refuses an empty title and says why', async () => {
    renderDialog()
    await pick('words_processed')
    fireEvent.change(title(), { target: { value: '  ' } })
    fireEvent.click(screen.getByTestId('create-chart-submit'))
    expect(await screen.findByText('Title is required')).toBeInTheDocument()
    expect(vi.mocked(createChart)).not.toHaveBeenCalled()
  })

  it('refuses to create a chart with no metric and says why', async () => {
    renderDialog()
    fireEvent.click(await screen.findByTestId('create-chart-submit'))
    expect(await screen.findByText('Pick a metric')).toBeInTheDocument()
    expect(vi.mocked(createChart)).not.toHaveBeenCalled()
  })

  it('offers the breakdown under its name, off by default', async () => {
    renderDialog()
    await pick('words_processed')
    expect(screen.getByText('Breakdown by')).toBeInTheDocument()
    expect(screen.getByTestId('chart-series-key')).toHaveTextContent('No breakdown')
  })

  describe('the assistant', () => {
    let prompts: ChatPromptDetail[]
    const listen = (e: Event) => prompts.push((e as CustomEvent<ChatPromptDetail>).detail)
    beforeEach(() => { prompts = []; window.addEventListener(CHAT_PROMPT_EVENT, listen) })
    afterEach(() => window.removeEventListener(CHAT_PROMPT_EVENT, listen))

    // Closing matters: a modal left open would sit over the chat it opened.
    it("closes the form and opens the chat with the assistant's suggestion", async () => {
      vi.mocked(suggestChart).mockResolvedValue({ source: 'ai', prompt: 'Chart QA issues by severity on "Operations".' })
      const { onClose } = renderDialog()
      fireEvent.click(await screen.findByTestId('builder-ask-assistant'))
      await waitFor(() => expect(prompts).toHaveLength(1))
      expect(vi.mocked(suggestChart)).toHaveBeenCalledWith(1)
      expect(onClose).toHaveBeenCalled()
      expect(prompts[0].prompt).toBe('Chart QA issues by severity on "Operations".')
    })

    it('falls back to an example for this dashboard when the suggestion fails', async () => {
      vi.mocked(suggestChart).mockRejectedValue(new Error('no key'))
      const { onClose } = renderDialog()
      fireEvent.click(await screen.findByTestId('builder-ask-assistant'))
      await waitFor(() => expect(prompts).toHaveLength(1))
      expect(onClose).toHaveBeenCalled()
      expect(prompts[0].prompt).toMatch(/^Chart .+ "Operations" dashboard\.$/)
    })
  })
})
