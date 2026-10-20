// DownloadMenu decides three things, and this file pins each:
//   1. which of the three options are offered vs. disabled, for a given scope
//   2. which jobs each option acts on (the deliverable skips jobs with nothing
//      translated — sending them would 500 or produce an empty file)
//   3. the counts users see before clicking, so a 12-file .zip is never a surprise
//
// Transport selection (single-item GET vs. project ZIP POST) lives in
// api/download.ts and is tested in api/download.test.ts — same split as
// JobPipelineActions vs. useJobPipelineActions.
//
// Anti-tautology RED proofs:
//   - Change `hasDeliverable` to `() => true` → "skips jobs with nothing
//     translated" fails (all three job ids get sent).
//   - Drop the `disabled={n === 0}` on the item → "disables the deliverable
//     when nothing is translated" fails.
//   - Gate `hasDeliverable` on approved segments → "offers the deliverable
//     for a job whose step has not confirmed anything yet" fails.
//   - Drop the dedupe in `uniqueFileIds` → "dedupes file ids" fails (two jobs
//     on one file would claim two source files).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { DownloadMenu } from './DownloadMenu'
import type { Job } from '../../api'
import { makeJob, makeJobCounts } from '@/test/fixtures/job'
import { menuItem } from '@/test/menu'

const downloadFiles = vi.fn().mockResolvedValue(undefined)

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>()
  return { ...actual, downloadFiles: (...args: unknown[]) => downloadFiles(...args) }
})


/** A job with a real deliverable (something approved). */
const done = (id: number, fileId: number) =>
  makeJob({ id, file_id: fileId, counts: makeJobCounts({ total: 10, approved: 10 }) })

/** A job that has segments but no translation yet. */
const inProgress = (id: number, fileId: number) =>
  makeJob({ id, file_id: fileId, counts: makeJobCounts({ total: 10, untranslated: 10 }) })

/** A translated job whose current step has not confirmed anything yet. */
const inReview = (id: number, fileId: number) =>
  makeJob({ id, file_id: fileId, counts: makeJobCounts({ total: 10, pre_translated: 10 }) })

async function open(jobs: Job[], fileIds: number[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={qc}>
      <DownloadMenu projectId={42} jobs={jobs} fileIds={fileIds} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByTestId('download-btn'))
  await menuItem('download-source')
}

beforeEach(() => vi.clearAllMocks())
afterEach(() => cleanup())

describe('DownloadMenu', () => {
  // Menu items are role=menuitem divs, not form controls, so jest-dom's
  // toBeDisabled() doesn't apply — base-ui signals the state via aria-disabled.
  const isDisabled = (testid: string) =>
    screen.getByTestId(testid).getAttribute('aria-disabled') === 'true'

  it('offers all three options for a finished job', async () => {
    await open([done(1, 10)], [10])
    expect(isDisabled('download-source')).toBe(false)
    expect(isDisabled('download-xliff')).toBe(false)
    expect(isDisabled('download-original')).toBe(false)
  })

  it('offers the deliverable for a job whose step has not confirmed anything yet', async () => {
    await open([inReview(1, 10)], [10])
    expect(isDisabled('download-source')).toBe(false)
  })

  it('disables the deliverable when nothing is translated, but keeps the bilingual hand-off', async () => {
    await open([inProgress(1, 10)], [10])
    // A bilingual XLIFF of an untranslated file is the point — that's what you
    // send the external translator.
    expect(isDisabled('download-source')).toBe(true)
    expect(isDisabled('download-xliff')).toBe(false)
    expect(isDisabled('download-original')).toBe(false)

    // And it really is inert — a disabled-looking item that still fires would
    // hand the user a 500 from the export endpoint.
    await userEvent.click(screen.getByTestId('download-source'))
    expect(downloadFiles).not.toHaveBeenCalled()
  })

  it('skips jobs with nothing translated when downloading deliverables', async () => {
    await open([done(1, 10), inProgress(2, 11), done(3, 12)], [10, 11, 12])
    await userEvent.click(screen.getByTestId('download-source'))

    expect(downloadFiles).toHaveBeenCalledTimes(1)
    const [projectId, kind, sel] = downloadFiles.mock.calls[0]
    expect(projectId).toBe(42)
    expect(kind).toBe('source')
    expect(sel.jobIds).toEqual([1, 3])
  })

  it('sends every job with segments for the bilingual export', async () => {
    await open([done(1, 10), inProgress(2, 11)], [10, 11])
    await userEvent.click(screen.getByTestId('download-xliff'))

    const [, kind, sel] = downloadFiles.mock.calls[0]
    expect(kind).toBe('xliff')
    expect(sel.jobIds).toEqual([1, 2])
  })

  it('dedupes file ids so two jobs on one file are one source file', async () => {
    // Same file, two target languages — the common multi-language project shape.
    await open([done(1, 10), done(2, 10)], [10, 10])
    expect(screen.getByTestId('download-original')).toHaveTextContent('Source file')
    expect(screen.getByTestId('download-original')).not.toHaveTextContent('(2)')

    await userEvent.click(screen.getByTestId('download-original'))
    const [, kind, sel] = downloadFiles.mock.calls[0]
    expect(kind).toBe('original')
    expect(sel.fileIds).toEqual([10])
  })

  it('warns that a multi-item pick arrives as one zip', async () => {
    await open([done(1, 10), done(2, 11)], [10, 11])
    const item = screen.getByTestId('download-source')
    expect(item).toHaveTextContent('Translated files (2)')
    expect(item).toHaveTextContent('one .zip')
  })

  it('disables the trigger when nothing is in scope', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <DownloadMenu projectId={42} jobs={[]} fileIds={[]} />
      </QueryClientProvider>,
    )
    expect(screen.getByTestId('download-btn')).toBeDisabled()
  })

  it('iconOnly: an icon named Download, with a tooltip, that opens the same menu', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <DownloadMenu projectId={42} jobs={[done(1, 10)]} fileIds={[10]} iconOnly />
      </QueryClientProvider>,
    )
    const trigger = screen.getByRole('button', { name: 'Download' })
    expect(trigger).toBe(screen.getByTestId('download-btn'))
    // No visible label: only the icon takes room in a table row.
    expect(trigger).toHaveTextContent(/^$/)

    await userEvent.hover(trigger)
    // The trigger has no text of its own, so this can only be the tooltip.
    expect(await screen.findByText('Download')).toBeInTheDocument()

    await userEvent.click(trigger)
    expect(await screen.findByTestId('download-source')).toBeInTheDocument()
    expect(screen.getByTestId('download-xliff')).toBeInTheDocument()
    expect(screen.getByTestId('download-original')).toBeInTheDocument()
  })
})
