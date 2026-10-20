// download.ts picks WHICH endpoint a download goes to, and that choice is a
// permissions decision, not a performance one:
//
//   1 item  → GET on the job/file route group, which linguists can reach.
//             The CAT editor always has exactly one job, so this is the path
//             that keeps vendors able to download their own work.
//   N items → POST to the manager-gated project endpoint, which answers with
//             one ZIP instead of N browser downloads.
//
// Get this backwards and the editor 403s for every vendor, so it's pinned here.
//
// Anti-tautology RED proofs:
//   - Change the `ids.length === 1` branch to `ids.length === 0` → the
//     single-item tests fail (they see a POST to /projects/…).
//   - Drop the `filename*=UTF-8''` branch in filenameFromResponse → the
//     non-ASCII filename test fails (saves as the mangled ASCII fallback).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

import { downloadFiles, filenameFromResponse, triggerDownload } from './download'

// jsdom has no object-URL support and won't navigate on an anchor click.
const clicked: string[] = []
const originalCreate = globalThis.URL.createObjectURL
const originalRevoke = globalThis.URL.revokeObjectURL

function okResponse(name = 'file.txt') {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'Content-Disposition': `attachment; filename="${name}"` }),
    blob: async () => new Blob(['data']),
  } as unknown as Response
}

const fetchMock = vi.fn()

beforeEach(() => {
  clicked.length = 0
  fetchMock.mockReset().mockResolvedValue(okResponse())
  vi.stubGlobal('fetch', fetchMock)
  globalThis.URL.createObjectURL = () => 'blob:stub'
  globalThis.URL.revokeObjectURL = () => {}
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push(this.download)
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  globalThis.URL.createObjectURL = originalCreate
  globalThis.URL.revokeObjectURL = originalRevoke
})

const lastCall = () => fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit]

describe('downloadFiles transport selection', () => {
  it('sends one job to the linguist-reachable job export route', async () => {
    await downloadFiles(42, 'source', { jobIds: [7], fileIds: [10] })
    const [url, init] = lastCall()
    expect(url).toBe('/api/jobs/7/export')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('passes the bilingual format through on the single-job route', async () => {
    await downloadFiles(42, 'xliff', { jobIds: [7], fileIds: [10] })
    expect(lastCall()[0]).toBe('/api/jobs/7/export?format=xliff')
  })

  it('sends one file to the source-download route', async () => {
    await downloadFiles(42, 'original', { jobIds: [], fileIds: [10] })
    expect(lastCall()[0]).toBe('/api/files/10/download')
  })

  it('batches several jobs into the project ZIP endpoint', async () => {
    await downloadFiles(42, 'source', { jobIds: [7, 8], fileIds: [10, 11] })
    const [url, init] = lastCall()
    expect(url).toBe('/api/projects/42/download')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ format: 'source', job_ids: [7, 8] })
  })

  it('batches several files by file id, not job id', async () => {
    await downloadFiles(42, 'original', { jobIds: [7, 8], fileIds: [10, 11] })
    const [url, init] = lastCall()
    expect(url).toBe('/api/projects/42/download')
    expect(JSON.parse(init.body as string)).toEqual({ format: 'original', file_ids: [10, 11] })
  })

  it('refuses an empty selection instead of firing a request', async () => {
    await expect(downloadFiles(42, 'source', { jobIds: [], fileIds: [] })).rejects.toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('surfaces the server error message word for word', async () => {
    // The bulk endpoint stops at the first file the user has to fix and names
    // it: "<file name>: <export error>". The toast shows this string as is.
    const message =
      'report.pdf: A PDF cannot be exported in its original form. Export as bilingual XLIFF instead.'
    fetchMock.mockResolvedValue({
      ok: false,
      status: 400,
      statusText: 'Bad Request',
      json: async () => ({ error: { message } }),
    } as unknown as Response)
    await expect(downloadFiles(42, 'source', { jobIds: [7, 8], fileIds: [] })).rejects.toMatchObject({
      name: 'ApiError',
      status: 400,
      message,
    })
  })

  it('saves under the server-provided filename', async () => {
    fetchMock.mockResolvedValue(okResponse('es_report.docx'))
    await downloadFiles(42, 'source', { jobIds: [7], fileIds: [] })
    expect(clicked).toEqual(['es_report.docx'])
  })
})

describe('filenameFromResponse', () => {
  it('prefers the RFC 5987 form so non-ASCII names survive', () => {
    // What httputil.SetAttachmentFilename emits: an ASCII-mangled fallback
    // plus the real name percent-encoded.
    const res = {
      headers: new Headers({
        'Content-Disposition':
          `attachment; filename="rapport_fran_ais.docx"; filename*=UTF-8''rapport_fran%C3%A7ais.docx`,
      }),
    } as Response
    expect(filenameFromResponse(res, 'fallback')).toBe('rapport_français.docx')
  })

  it('falls back to the plain form when there is no starred one', () => {
    const res = {
      headers: new Headers({ 'Content-Disposition': 'attachment; filename="plain.txt"' }),
    } as Response
    expect(filenameFromResponse(res, 'fallback')).toBe('plain.txt')
  })

  it('falls back to the caller default when the header is absent', () => {
    expect(filenameFromResponse({ headers: new Headers() } as Response, 'fallback')).toBe('fallback')
  })
})


describe('triggerDownload', () => {
  // A plain download, the same as every server-generated file: it lands in the
  // browser's downloads, under the name given, with nothing else to confirm.
  it('downloads the blob under the given name', () => {
    triggerDownload(new Blob(['x']), 'Words per week 2026-09-24.xlsx')
    expect(clicked).toEqual(['Words per week 2026-09-24.xlsx'])
  })
})
