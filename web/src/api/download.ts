import { ApiError, BASE, getToken } from './request';

/**
 * Shared helpers for authenticated file downloads: auth header, blob,
 * Content-Disposition parse, anchor click, revoke.
 */

/**
 * Reads the server-provided filename, preferring RFC 5987 `filename*=UTF-8''`
 * over the ASCII-mangled `filename=` fallback. The backend emits both (see
 * `httputil.SetAttachmentFilename`), and only the starred form survives
 * non-ASCII names like "rapport_français.docx".
 */
export function filenameFromResponse(res: Response, fallback: string): string {
  const cd = res.headers.get('Content-Disposition');
  if (!cd) return fallback;
  const starred = cd.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  if (starred) {
    try {
      return decodeURIComponent(starred);
    } catch {
      /* malformed percent-encoding — fall through to the plain form */
    }
  }
  return cd.match(/filename="(.+)"/)?.[1] ?? fallback;
}

/** Hands a blob to the browser as a download. */
export function triggerDownload(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

/** Turns a successful response into a browser download. */
export async function downloadBlobResponse(res: Response, fallbackName: string): Promise<void> {
  const blob = await res.blob();
  triggerDownload(blob, filenameFromResponse(res, fallbackName));
}


/**
 * Fetches `path` with auth. Non-2xx responses throw an `ApiError` carrying the
 * server's message, so callers can toast it.
 */
async function fetchAuthed(path: string, init?: RequestInit): Promise<Response> {
  const headers: Record<string, string> = { ...(init?.headers as Record<string, string>) };
  const token = getToken();
  if (token && token !== '__cookie_auth__') headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${path}`, { ...init, headers, credentials: 'same-origin' });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: { message: res.statusText } }));
    throw new ApiError(res.status, err.error?.message || res.statusText, err);
  }
  return res;
}

/** Fetches `path` with auth and saves the body to disk. */
export async function downloadFrom(
  path: string,
  fallbackName: string,
  init?: RequestInit,
): Promise<void> {
  await downloadBlobResponse(await fetchAuthed(path, init), fallbackName);
}

/** Fetches `path` with auth as a blob, e.g. an image the page shows. */
export async function fetchBlob(path: string): Promise<Blob> {
  return (await fetchAuthed(path)).blob();
}

/**
 * What the user picked in the Download menu:
 * - `source`   — the deliverable, rebuilt in the uploaded format (DOCX→DOCX)
 * - `xliff`    — bilingual XLIFF 1.2 for hand-off to other CAT tools
 * - `original` — the untouched upload
 */
export type DownloadKind = 'source' | 'xliff' | 'original';

export interface DownloadSelection {
  /** Job ids for `source` / `xliff` (a deliverable is per file × target language). */
  jobIds: number[];
  /** File ids for `original` (the upload has no target language). */
  fileIds: number[];
}

/** The untouched upload for one file. Reachable by linguists (file route group). */
export const downloadSourceFile = (fileId: number) =>
  downloadFrom(`/files/${fileId}/download`, 'source-file');

/** One job's deliverable or bilingual XLIFF. Reachable by linguists (job route group). */
export const downloadJobExport = (jobId: number, kind: 'source' | 'xliff') =>
  downloadFrom(`/jobs/${jobId}/export${kind === 'source' ? '' : `?format=${kind}`}`, 'export');

/**
 * Downloads the selection, picking the transport by item count:
 *
 * - **1 item** → the single-item GET on the job/file route group. Those groups
 *   are linguist-reachable, so the CAT editor's menu (always one job) works for
 *   vendors; the project route group would 403 them.
 * - **>1 item** → one ZIP from the manager-gated project endpoint, instead of N
 *   browser downloads (which Chrome/Safari prompt on or block outright).
 */
export async function downloadFiles(
  projectId: number,
  kind: DownloadKind,
  sel: DownloadSelection,
): Promise<void> {
  const ids = kind === 'original' ? sel.fileIds : sel.jobIds;
  if (ids.length === 0) throw new Error('Nothing selected to download');

  if (ids.length === 1) {
    return kind === 'original'
      ? downloadSourceFile(ids[0])
      : downloadJobExport(ids[0], kind);
  }

  return downloadFrom(`/projects/${projectId}/download`, 'download.zip', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(
      kind === 'original' ? { format: kind, file_ids: sel.fileIds } : { format: kind, job_ids: sel.jobIds },
    ),
  });
}
