import { request, getToken, BASE } from './request';
import { downloadFrom } from './download';

// ─── Terminology ───

export interface TermBase {
  id: number;
  name: string;
  description: string;
  source_language: string;
  target_languages: string[]; // derived from the base's terms (minus source)
  entry_count: number;
  languages: string[]; // distinct languages actually present (concept model)
  created_at: string;
  updated_at: string;
  // Creator attribution. created_by is the user id (null for bases created
  // before attribution existed); created_by_name is the resolved display name
  // (LEFT JOIN users, list endpoint only) and is '' when unknown.
  created_by?: number | null;
  created_by_name?: string;
}

// ─── Concept-centric model (Phase 4 concept editor) ───

export interface ConceptTerm {
  id: number;
  language: string;
  text: string;
  status: string;
  part_of_speech: string;
  notes: string;
  created_at: string;
  updated_at: string;
  created_by: string; // user name, "" if unknown
  updated_by: string;
}

export interface Concept {
  id: number;
  definition: string;
  domain: string;
  created_at: string;
  updated_at: string;
  created_by: string; // user name, "" if unknown
  updated_by: string;
  terms: ConceptTerm[];
}

export interface ConceptsPage {
  data: Concept[];
  cursor: number;
  has_more: boolean;
  total: number;
}

// A term payload for create/update (no id → new term).
export type ConceptTermInput = {
  language: string;
  text: string;
  status: string;
  part_of_speech?: string;
  notes?: string;
};

export const listConcepts = (baseId: number, opts?: { limit?: number; cursor?: number; q?: string; lang?: string[] }): Promise<ConceptsPage> => {
  const qs = new URLSearchParams();
  if (opts?.limit) qs.set('limit', String(opts.limit));
  if (opts?.cursor) qs.set('cursor', String(opts.cursor));
  if (opts?.q) qs.set('q', opts.q);
  opts?.lang?.forEach(l => qs.append('lang', l));
  const suffix = qs.toString() ? `?${qs}` : '';
  return request<ConceptsPage>(`/terminology/${baseId}/concepts${suffix}`);
};

export const getConcept = (baseId: number, conceptId: number) =>
  request<{ data: Concept }>(`/terminology/${baseId}/concepts/${conceptId}`).then(r => r.data);

export const createConcept = (baseId: number, data: { definition: string; domain: string; terms: ConceptTermInput[] }) =>
  request<{ data: Concept }>(`/terminology/${baseId}/concepts`, { method: 'POST', body: JSON.stringify(data) }).then(r => r.data);

export const updateConcept = (baseId: number, conceptId: number, data: { definition: string; domain: string }) =>
  request<void>(`/terminology/${baseId}/concepts/${conceptId}`, { method: 'PATCH', body: JSON.stringify(data) });

export const deleteConcept = (baseId: number, conceptId: number) =>
  request<void>(`/terminology/${baseId}/concepts/${conceptId}`, { method: 'DELETE' });

export const addConceptTerm = (baseId: number, conceptId: number, term: ConceptTermInput) =>
  request<{ data: ConceptTerm }>(`/terminology/${baseId}/concepts/${conceptId}/terms`, { method: 'POST', body: JSON.stringify(term) }).then(r => r.data);

export const updateConceptTerm = (baseId: number, conceptId: number, termId: number, term: ConceptTermInput) =>
  request<void>(`/terminology/${baseId}/concepts/${conceptId}/terms/${termId}`, { method: 'PATCH', body: JSON.stringify(term) });

export const deleteConceptTerm = (baseId: number, conceptId: number, termId: number) =>
  request<void>(`/terminology/${baseId}/concepts/${conceptId}/terms/${termId}`, { method: 'DELETE' });

export interface TermLookupResult {
  id: number;
  concept_id: number; // groups synonyms: same concept_id = alternatives for one meaning
  source_term: string;
  target_term: string;
  mt_translation: string;
  status: string;
  /** Status of the SOURCE-language term that matched (the returned `status` is
   *  the TARGET term's). "forbidden" here means the source used a discouraged
   *  input phrasing — inert on the unchangeable source, so it's never painted
   *  as a violation (ADR forbidden-is-target-only). */
  source_status?: string;
  notes: string;
  domain: string;
  term_base_id: number;
  term_base_name: string;
  priority: number;
}

export const listTermBases = () =>
  request<{ data: TermBase[] }>('/terminology').then(r => r.data);

export const createTermBase = (name: string, description: string, source_language: string) =>
  request<{ data: TermBase }>('/terminology', {
    method: 'POST',
    body: JSON.stringify({ name, description, source_language }),
  }).then(r => r.data);

export const getTermBase = (id: number) =>
  request<{ data: { term_base: TermBase; entry_count: number } }>(`/terminology/${id}`).then(r => r.data);

export const updateTermBase = (id: number, name: string, description: string) =>
  request<{ data: TermBase }>(`/terminology/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name, description }),
  }).then(r => r.data);

export const deleteTermBase = (id: number) =>
  request<void>(`/terminology/${id}`, { method: 'DELETE' });

export type TermImportResult = { imported: number; skipped: number }

// importTermFile uploads a glossary file (CSV or TBX) to the given endpoint with
// XHR progress + abort support. importTermCSV/importTermTBX are thin wrappers.
const importTermFile = (
  baseId: number,
  file: File,
  endpoint: string,
  onUploadProgress?: (pct: number) => void,
): { promise: Promise<TermImportResult>; abort: () => void } => {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<TermImportResult>((resolve, reject) => {
    const form = new FormData()
    form.append('file', file)
    xhr.open('POST', `${BASE}/terminology/${baseId}/${endpoint}`)
    const token = getToken()
    if (token && token !== '__cookie_auth__') xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.withCredentials = true

    if (onUploadProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) {
          onUploadProgress(Math.round((e.loaded / e.total) * 100))
        }
      }
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try { resolve(JSON.parse(xhr.responseText)) } catch { reject(new Error('Invalid response')) }
      } else {
        try {
          const err = JSON.parse(xhr.responseText)
          reject(new Error(err.error?.message || xhr.statusText))
        } catch { reject(new Error(xhr.statusText)) }
      }
    }
    xhr.onerror = () => reject(new Error('Upload failed'))
    xhr.onabort = () => reject(new Error('Upload cancelled'))
    xhr.send(form)
  })
  return { promise, abort: () => xhr.abort() }
}

// source/target set the default language pair for rows that don't carry their
// own source_language/target_language column (see HandleImportCSV).
export const importTermCSV = (
  baseId: number,
  file: File,
  onUploadProgress?: (pct: number) => void,
  opts?: { source?: string; target?: string },
) => {
  const qs = new URLSearchParams()
  if (opts?.source) qs.set('source', opts.source)
  if (opts?.target) qs.set('target', opts.target)
  const suffix = qs.toString() ? `?${qs}` : ''
  return importTermFile(baseId, file, `import${suffix}`, onUploadProgress)
}

export const importTermTBX = (baseId: number, file: File, onUploadProgress?: (pct: number) => void) =>
  importTermFile(baseId, file, 'import-tbx', onUploadProgress)

// exportTermFile downloads a glossary export from the given endpoint as a blob.
const exportTermFile = (baseId: number, baseName: string, endpoint: string, ext: string) =>
  downloadFrom(`/terminology/${baseId}/${endpoint}`, `${baseName}.${ext}`);

// Distinct languages present in a base (superset of its default source/target
// pair, since the base is multilingual). Drives the CSV export language picker.
export const listTermBaseLanguages = (baseId: number) =>
  request<{ data: string[] }>(`/terminology/${baseId}/languages`).then(r => r.data);

// targets is optional: omit (or pass empty) to export every language present
// (adds a target_language column); one target → compact bilingual pair; several
// → those languages only, with a target_language column.
export const exportTermCSV = (baseId: number, baseName: string, opts?: { source?: string; targets?: string[] }) => {
  const qs = new URLSearchParams()
  if (opts?.source) qs.set('source', opts.source)
  for (const t of opts?.targets ?? []) qs.append('target', t)
  const suffix = qs.toString() ? `?${qs}` : ''
  return exportTermFile(baseId, baseName, `export${suffix}`, 'csv')
}

export const exportTermTBX = (baseId: number, baseName: string) =>
  exportTermFile(baseId, baseName, 'export-tbx', 'tbx')

export const lookupTerms = (projectId: number, sourceLang: string, targetLang: string, sourceText: string) =>
  request<{ data: TermLookupResult[] }>(`/terminology/lookup?project_id=${projectId}&source_language=${sourceLang}&target_language=${targetLang}&source_text=${encodeURIComponent(sourceText)}`).then(r => r.data);
