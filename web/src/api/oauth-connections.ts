// Connected apps: the caller's OAuth grants.
import { request } from './request'

export interface OAuthConnection {
  family_id: string
  client_name: string
  scope: 'read' | 'write'
  created_at: string
  last_used_at: string | null
}

export const listOAuthConnections = (): Promise<OAuthConnection[]> =>
  request<{ data: OAuthConnection[] }>('/oauth/connections').then(r => r.data)

export const revokeOAuthConnection = (familyID: string): Promise<void> =>
  request(`/oauth/connections/${familyID}`, { method: 'DELETE' })
