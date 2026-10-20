// useUploadQueue — the shared file-upload loop behind the project page and
// the New Project wizard's Files card. The semantics worth pinning are the
// ones a caller cannot see from the outside: uploads run one at a time, a
// batch queued while another is in flight extends it instead of starting a
// second loop, and progress returns to null when the queue drains.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import type { UploadResult, XLSXInspection } from '../api'

const api = vi.hoisted(() => ({
  uploadFile: vi.fn(),
  inspectXLSX: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number
    constructor(status: number, message: string) {
      super(message)
      this.status = status
    }
  },
}))
vi.mock('../api', () => api)
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }))

import { useUploadQueue } from './useUploadQueue'
import { toast } from 'sonner'

const file = (name: string) => new File(['x'], name, { type: 'text/plain' })
const result = (name: string): UploadResult => ({ data: { name } } as unknown as UploadResult)

/** A promise plus the handle to settle it — one per queued upload. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: Error) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('useUploadQueue', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('uploads one file at a time, in order', async () => {
    const first = deferred<UploadResult>()
    const second = deferred<UploadResult>()
    api.uploadFile.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const { result: hook } = renderHook(() => useUploadQueue())

    act(() => { hook.current.start(7, [file('a.txt'), file('b.txt')]) })

    // The second upload has not started while the first is in flight.
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalledTimes(1))
    expect(api.uploadFile.mock.calls[0][0]).toBe(7)
    expect((api.uploadFile.mock.calls[0][1] as File).name).toBe('a.txt')
    expect(hook.current.progress).toMatchObject({ current: 1, total: 2, fileName: 'a.txt' })

    await act(async () => { first.resolve(result('a.txt')) })
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalledTimes(2))
    expect((api.uploadFile.mock.calls[1][1] as File).name).toBe('b.txt')

    await act(async () => { second.resolve(result('b.txt')) })
    await waitFor(() => expect(hook.current.progress).toBeNull())
    expect(hook.current.uploading).toBe(false)
  })

  it('extends a running batch instead of starting a second loop', async () => {
    const first = deferred<UploadResult>()
    const rest = [deferred<UploadResult>(), deferred<UploadResult>()]
    api.uploadFile
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(rest[0].promise)
      .mockReturnValueOnce(rest[1].promise)
    const { result: hook } = renderHook(() => useUploadQueue())

    act(() => { hook.current.start(7, [file('a.txt')]) })
    await waitFor(() => expect(hook.current.progress?.total).toBe(1))

    act(() => { hook.current.start(7, [file('b.txt'), file('c.txt')]) })
    // Still one upload in flight; the total grew rather than a second loop
    // racing the first.
    expect(api.uploadFile).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(hook.current.progress?.total).toBe(3))

    await act(async () => { first.resolve(result('a.txt')) })
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalledTimes(2))
    await act(async () => { rest[0].resolve(result('b.txt')) })
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalledTimes(3))
    await act(async () => { rest[1].resolve(result('c.txt')) })
    await waitFor(() => expect(hook.current.progress).toBeNull())
  })

  it('reports each finished upload to the caller', async () => {
    const done = result('a.txt')
    api.uploadFile.mockResolvedValue(done)
    const onUploaded = vi.fn()
    const { result: hook } = renderHook(() => useUploadQueue(onUploaded))

    await act(async () => { hook.current.start(42, [file('a.txt')]) })
    await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1))
    const [projectId, uploaded, res] = onUploaded.mock.calls[0]
    expect(projectId).toBe(42)
    expect((uploaded as File).name).toBe('a.txt')
    expect(res).toBe(done)
  })

  it('a failed upload does not stop the ones behind it', async () => {
    api.uploadFile
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(result('b.txt'))
    const onUploaded = vi.fn()
    const { result: hook } = renderHook(() => useUploadQueue(onUploaded))

    await act(async () => { hook.current.start(7, [file('a.txt'), file('b.txt')]) })
    await waitFor(() => expect(hook.current.progress).toBeNull())

    expect(api.uploadFile).toHaveBeenCalledTimes(2)
    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('a.txt'))
    // Only the file that landed is reported.
    expect(onUploaded).toHaveBeenCalledTimes(1)
    expect((onUploaded.mock.calls[0][1] as File).name).toBe('b.txt')
  })

  it('surfaces the server warnings a finished upload carries', async () => {
    api.uploadFile.mockResolvedValue({ data: {}, warnings: ['pre-translation could not be queued for fr'] } as unknown as UploadResult)
    const { result: hook } = renderHook(() => useUploadQueue())

    await act(async () => { hook.current.start(7, [file('a.txt')]) })
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith(
      'pre-translation could not be queued for fr', expect.anything(),
    ))
  })

  it('an empty batch is not a batch', () => {
    const { result: hook } = renderHook(() => useUploadQueue())
    act(() => { hook.current.start(7, []) })
    expect(api.uploadFile).not.toHaveBeenCalled()
    expect(hook.current.progress).toBeNull()
  })

  describe('an .xlsx without language codes in row 1', () => {
    const xlsx = (name: string) => new File(['x'], name)
    const layout = { sheets: [{ name: 'Sheet1', header_row: true, columns: ['B'] }] }
    const inspection: XLSXInspection = {
      bilingual: false,
      sheets: [{ name: 'Sheet1', columns: [{ letter: 'A' }, { letter: 'B' }] }],
      suggested_layout: layout,
    }

    it('waits for its columns, then uploads with them', async () => {
      api.inspectXLSX.mockResolvedValueOnce(inspection)
      api.uploadFile.mockResolvedValueOnce(result('catalog.xlsx'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('catalog.xlsx')], { analyze: true }) })
      await waitFor(() => expect(hook.current.layoutQuestion?.file.name).toBe('catalog.xlsx'))
      expect(hook.current.layoutQuestion?.suggested).toEqual(layout)
      expect(api.uploadFile).not.toHaveBeenCalled()

      act(() => { hook.current.answerLayout(layout) })
      await waitFor(() => expect(api.uploadFile).toHaveBeenCalledOnce())
      expect(api.uploadFile.mock.calls[0][3]).toEqual({ analyze: true, xlsxLayout: layout })
      expect(hook.current.layoutQuestion).toBeNull()
      await waitFor(() => expect(hook.current.progress).toBeNull())
    })

    it('uploads cell by cell on null and skips the file on skip', async () => {
      api.inspectXLSX.mockResolvedValue(inspection)
      api.uploadFile.mockResolvedValue(result('a.xlsx'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('a.xlsx'), xlsx('b.xlsx')]) })
      await waitFor(() => expect(hook.current.layoutQuestion?.file.name).toBe('a.xlsx'))
      act(() => { hook.current.answerLayout(null) })
      await waitFor(() => expect(hook.current.layoutQuestion?.file.name).toBe('b.xlsx'))
      act(() => { hook.current.answerLayout('skip') })
      await waitFor(() => expect(hook.current.progress).toBeNull())

      expect(api.uploadFile).toHaveBeenCalledOnce()
      expect((api.uploadFile.mock.calls[0][1] as File).name).toBe('a.xlsx')
      expect(api.uploadFile.mock.calls[0][3]).toEqual({ analyze: undefined, xlsxLayout: undefined })
    })

    it('takes an answer given with the batch without asking', async () => {
      const file = xlsx('given.xlsx')
      api.uploadFile.mockResolvedValueOnce(result('given.xlsx'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [file], { layouts: new Map([[file, layout]]) }) })
      await waitFor(() => expect(api.uploadFile).toHaveBeenCalledOnce())
      expect(api.inspectXLSX).not.toHaveBeenCalled()
      expect(hook.current.layoutQuestion).toBeNull()
      expect(api.uploadFile.mock.calls[0][3]).toEqual({ analyze: undefined, xlsxLayout: layout })
    })

    it('reads the workbook again after a failure that is not the workbook\'s', async () => {
      api.inspectXLSX.mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValueOnce(inspection)
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('catalog.xlsx')]) })
      await waitFor(() => expect(hook.current.layoutQuestion?.file.name).toBe('catalog.xlsx'), { timeout: 3000 })
      expect(api.inspectXLSX).toHaveBeenCalledTimes(2)
      expect(api.uploadFile).not.toHaveBeenCalled()
    })

    it('skips the file when it still cannot be read, rather than upload it unasked', async () => {
      api.inspectXLSX.mockRejectedValue(new api.ApiError(503, 'Service unavailable'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('catalog.xlsx')]) })
      await waitFor(() => expect(hook.current.progress).toBeNull(), { timeout: 3000 })
      expect(api.inspectXLSX).toHaveBeenCalledTimes(3)
      expect(api.uploadFile).not.toHaveBeenCalled()
      expect(toast.error).toHaveBeenCalledWith('Could not read catalog.xlsx. Add it again to upload it.')
    })

    it('uploads a workbook the server refuses, for the upload to report it', async () => {
      api.inspectXLSX.mockRejectedValueOnce(new api.ApiError(400, 'could not read the xlsx file'))
      api.uploadFile.mockResolvedValueOnce(result('broken.xlsx'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('broken.xlsx')]) })
      await waitFor(() => expect(api.uploadFile).toHaveBeenCalledOnce())
      expect(api.inspectXLSX).toHaveBeenCalledTimes(1)
      expect(api.uploadFile.mock.calls[0][3]).toEqual({ analyze: undefined, xlsxLayout: undefined })
    })

    it('asks nothing for a bilingual workbook', async () => {
      api.inspectXLSX.mockResolvedValueOnce({ ...inspection, bilingual: true, suggested_layout: undefined })
      api.uploadFile.mockResolvedValueOnce(result('bi.xlsx'))
      const { result: hook } = renderHook(() => useUploadQueue())

      act(() => { hook.current.start(7, [xlsx('bi.xlsx')]) })
      await waitFor(() => expect(api.uploadFile).toHaveBeenCalledOnce())
      expect(hook.current.layoutQuestion).toBeNull()
    })
  })
})
