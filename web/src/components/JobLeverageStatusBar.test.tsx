// The editor's bottom bar: the job's size first, then the live leverage
// bands. The target language lives in the breadcrumb, not here.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobLeverageStatusBar from './JobLeverageStatusBar'

vi.mock('../api/analyses', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/analyses')>()
  const band = (words: number) => ({ segments: 1, words, words_needs_review: 0 })
  return {
    ...actual,
    getJobLeverage: vi.fn().mockResolvedValue({
      job_id: 7, target_language: 'de',
      tm_101: band(3), tm_100: band(6), tm_85_99: band(11), tm_65_84: band(15), new: band(110), repetitions: band(0),
    }),
  }
})

afterEach(cleanup)

function renderBar() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <JobLeverageStatusBar jobId={7} segmentCount={31} wordCount={145} />
    </QueryClientProvider>,
  )
}

describe('JobLeverageStatusBar', () => {
  it('opens with the job size, then the leverage bands, and no target language', async () => {
    renderBar()
    await screen.findByText('110 words')
    const bar = screen.getByTestId('job-leverage-bar')
    const text = bar.textContent ?? ''
    expect(text.indexOf('31 segments')).toBe(0)
    expect(text.indexOf('145 words')).toBeGreaterThan(0)
    expect(text.indexOf('145 words')).toBeLessThan(text.indexOf('Leverage:'))
    expect(text).not.toMatch(/→\s*de/)
  })

  it('ends with repetitions, after the 101% band', async () => {
    renderBar()
    await screen.findByText('110 words')
    const text = screen.getByTestId('job-leverage-bar').textContent ?? ''
    expect(text.indexOf('101%:')).toBeLessThan(text.indexOf('Repetitions:'))
    expect(text.indexOf('New:')).toBeLessThan(text.indexOf('101%:'))
  })
})
