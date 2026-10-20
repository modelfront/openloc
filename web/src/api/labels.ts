// Project label API client.
//
// A label is a tenant-named classifier ("Country") with a fixed set of allowed
// values ("Germany"); a project picks at most one value per label. Reads always
// return a label with its values, so the client never stitches two lists.
//
// Types are handwritten rather than taken from the tygo output for the same
// reason as Customer: the response shape here is a DTO assembled by the
// handler, not a table row, so no generated type describes it.

import { request } from './request'

export interface LabelValue {
  id: number
  value: string
  /** A project has chosen it. It can still be deleted; its label cannot. */
  in_use: boolean
  /** How many projects picked it — they lose the pick if it is deleted. */
  project_count: number
}

export interface Label {
  id: number
  name: string
  /** Built in (Customer, Domain): its values are editable, the label is not deletable. */
  system: boolean
  /** Some project carries one of its values, so it cannot be deleted. */
  in_use: boolean
  values: LabelValue[]
}

/** One project's pick, with the names already resolved for rendering. */
export interface ProjectLabel {
  label_definition_id: number
  label_value_id: number
  label_name: string
  label_value: string
}

export interface LabelValueProjectCount {
  project_count: number
}

export const listLabels = () =>
  request<{ data: Label[] }>('/labels').then(r => r.data)

export const createLabel = (name: string, values: string[] = []) =>
  request<{ data: Label }>('/labels', {
    method: 'POST',
    body: JSON.stringify({ name, values }),
  }).then(r => r.data)

export const deleteLabel = (id: number) =>
  request<void>(`/labels/${id}`, { method: 'DELETE' })

export const createLabelValue = (labelId: number, value: string) =>
  request<{ data: LabelValue }>(`/labels/${labelId}/values`, {
    method: 'POST',
    body: JSON.stringify({ value }),
  }).then(r => r.data)

export const deleteLabelValue = (labelId: number, valueId: number) =>
  request<void>(`/labels/${labelId}/values/${valueId}`, { method: 'DELETE' })

export const getLabelValueProjectCount = (labelId: number, valueId: number) =>
  request<{ data: LabelValueProjectCount }>(`/labels/${labelId}/values/${valueId}/project-count`).then(r => r.data)

export const listProjectLabels = (projectId: number) =>
  request<{ data: ProjectLabel[] }>(`/projects/${projectId}/labels`).then(r => r.data)

/** Replaces the project's whole set of picks — what the form shows is the request. */
export const setProjectLabels = (projectId: number, labels: { label_definition_id: number; label_value_id: number }[]) =>
  request<{ data: ProjectLabel[] }>(`/projects/${projectId}/labels`, {
    method: 'PUT',
    body: JSON.stringify({ labels }),
  }).then(r => r.data)

export const listTemplateLabels = (templateId: number) =>
  request<{ data: ProjectLabel[] }>(`/templates/${templateId}/labels`).then(r => r.data)

/** Replaces the template's whole set of presets — same contract as a project's. */
export const setTemplateLabels = (templateId: number, labels: { label_definition_id: number; label_value_id: number }[]) =>
  request<{ data: ProjectLabel[] }>(`/templates/${templateId}/labels`, {
    method: 'PUT',
    body: JSON.stringify({ labels }),
  }).then(r => r.data)
