import { ApiError, getToken } from './request'

const BASE = '/api'

/**
 * Conflict report returned by `POST /api/files/{fileID}/reimport-xliff`.
 * Mirrors `internal/file/reimport.go::ReimportXLIFFResult` 1:1 so refactors on
 * the Go side surface here as TS errors immediately.
 */
export interface ReimportXLIFFResult {
  updated: number
  skipped_locked: number
  /** Segments someone else has open in the editor; never written. */
  skipped_held: number
  skipped_approved: number
  untouched_no_change: number
  extra_in_upload: number
  missing_from_upload: number
  force_required: boolean
  applied: boolean

  skipped_locked_keys?: string[]
  skipped_held_keys?: string[]
  skipped_approved_keys?: string[]
  extra_in_upload_keys?: string[]
  missing_from_upload_keys?: string[]

  skipped_locked_truncated?: boolean
  skipped_held_truncated?: boolean
  skipped_approved_truncated?: boolean
  extra_in_upload_truncated?: boolean
  missing_from_upload_truncated?: boolean
}

export interface ReimportXLIFFOptions {
  /** Target language code; required by the server. Sent normalized server-side. */
  lang: string
  /** When true, server overrides translations.status='approved' (locks remain absolute). */
  force?: boolean
}

/**
 * POST `/api/files/{fileID}/reimport-xliff` with the supplied XLIFF file.
 *
 * Uses XHR like the other multipart uploads (`uploadFile`, `importTMX`).
 * Status-code branching is explicit so callers can distinguish:
 *   - 200 → conflict report, may have `force_required=true`
 *   - 400 → user-fixable input error (parse fail, dup keys, oversized lang param)
 *   - 409 → the task cannot take edits now (in error, or its AI step is running)
 *   - 413 → too many segments, body carries the cap message
 *   - 5xx → server fault
 */
export const reimportXLIFF = (
  fileId: number,
  file: File,
  opts: ReimportXLIFFOptions,
): Promise<ReimportXLIFFResult> => {
  return new Promise<ReimportXLIFFResult>((resolve, reject) => {
    const params = new URLSearchParams()
    params.set('lang', opts.lang)
    if (opts.force) params.set('force', 'true')

    const xhr = new XMLHttpRequest()
    const form = new FormData()
    form.append('file', file)
    xhr.open('POST', `${BASE}/files/${fileId}/reimport-xliff?${params.toString()}`)
    const token = getToken()
    if (token && token !== '__cookie_auth__') {
      xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    }
    xhr.withCredentials = true

    xhr.onload = () => {
      // Try to parse the body once — both success and error responses use JSON.
      let body: unknown
      try {
        body = xhr.responseText ? JSON.parse(xhr.responseText) : undefined
      } catch {
        reject(new ApiError(xhr.status, 'Invalid response from server'))
        return
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        const data = (body as { data?: ReimportXLIFFResult } | undefined)?.data
        if (!data) {
          reject(new ApiError(xhr.status, 'Malformed response: missing data'))
          return
        }
        resolve(data)
        return
      }
      // Error path. Carry the full body for the caller.
      const errMsg =
        (body as { error?: { message?: string } | string } | undefined)?.error
      const message =
        typeof errMsg === 'string'
          ? errMsg
          : errMsg?.message || xhr.statusText || 'Import failed'
      reject(new ApiError(xhr.status, message, body))
    }
    xhr.onerror = () => reject(new ApiError(0, 'Network error during import'))
    xhr.onabort = () => reject(new ApiError(0, 'Import cancelled'))
    xhr.send(form)
  })
}
