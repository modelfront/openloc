// Customer API client. Customers are tenant-scoped and
// soft-deletable.
//
// `Customer` is hand-written, not imported from `types/generated.ts`: tygo
// emits nullable timestamptz as `string`, not `string | null`.

import { request } from './request'

export interface Customer {
  id: number
  tenant_id: number
  name: string
  created_at: string
  updated_at: string
  // Always null in list/get responses (those queries filter `WHERE deleted_at IS NULL`).
  deleted_at: string | null
}

export interface CustomerProjectCount {
  project_count: number
}

export const listCustomers = () =>
  request<{ data: Customer[] }>('/customers').then(r => r.data)

export const getCustomer = (id: number) =>
  request<{ data: Customer }>(`/customers/${id}`).then(r => r.data)

export const createCustomer = (name: string) =>
  request<{ data: Customer }>('/customers', {
    method: 'POST',
    body: JSON.stringify({ name }),
  }).then(r => r.data)

export const renameCustomer = (id: number, name: string) =>
  request<{ data: Customer }>(`/customers/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  }).then(r => r.data)

export const deleteCustomer = (id: number) =>
  request<void>(`/customers/${id}`, { method: 'DELETE' })

export const getCustomerProjectCount = (id: number) =>
  request<{ data: CustomerProjectCount }>(`/customers/${id}/project-count`).then(r => r.data)
