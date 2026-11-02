import { request } from './request'
import { downloadFrom } from './download'
import type * as segmentation from '../types/segmentation'
import type { Input, Preview, PreviewInput, Profile } from '../types/segmentationapi'

// Segmentation profiles: the built-ins (read-only) and the org's own, each for
// one source language or for any. Every member reads and previews; owners,
// admins and project managers write.
export type { Preview as SegmentationPreview, Profile as SegmentationProfile }
export type { PreviewDecision as SegmentationPreviewDecision } from '../types/segmentationapi'
export type SegmentationRules = segmentation.RuleSet
export type SegmentationRule = segmentation.Rule
export type SegmentationMode = 'sentence' | 'paragraph'
export type SRXImport = segmentation.SRXImport

const base = '/segmentation-profiles'

/** With a source language: only the profiles that suit it, the one a new
 *  project in it starts with marked `suggested`. */
export const listSegmentationProfiles = (sourceLanguage?: string) =>
  request<{ data: Profile[] }>(
    `${base}${sourceLanguage ? `?source_language=${encodeURIComponent(sourceLanguage)}` : ''}`,
  ).then(r => r.data)

export const getSegmentationProfile = (id: number) =>
  request<{ data: Profile }>(`${base}/${id}`).then(r => r.data)

export const createSegmentationProfile = (input: Input) =>
  request<{ data: Profile }>(base, { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateSegmentationProfile = (id: number, input: Input) =>
  request<{ data: Profile }>(`${base}/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteSegmentationProfile = (id: number) =>
  request<void>(`${base}/${id}`, { method: 'DELETE' })

export const previewSegmentation = (input: PreviewInput) =>
  request<{ data: Preview }>(`${base}/preview`, { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

/** Reads an SRX file into unsaved drafts and warnings; nothing is stored. */
export const parseSRX = (file: File) => {
  const form = new FormData()
  form.append('file', file)
  return request<{ data: SRXImport }>(`${base}/srx/parse`, { method: 'POST', body: form }).then(r => r.data)
}

export const exportSegmentationProfileSRX = (id: number, name: string) =>
  downloadFrom(`${base}/${id}/srx`, `${name}.srx`)
