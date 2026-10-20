import { request } from './request';

// Project history / audit log. Hand-written wire types:
// the backend DTO joins the actor and carries free-form jsonb details, so it
// isn't a sqlcgen model tygo could generate.

/** One old→new field pair inside details.changes. */
export interface FieldChange {
  from: unknown;
  to: unknown;
}

export interface ProjectEventActor {
  id: number;
  name: string;
  email: string;
}

export interface ProjectEvent {
  id: number;
  event_type: string;
  /** null = system action, or the user was since deleted (see details.actor_name). */
  actor: ProjectEventActor | null;
  file_id: number | null;
  job_id: number | null;
  /** Free-form per-event payload. Known keys are documented per event type
   *  in internal/projectevents/events.go; diffs use
   *  {changes: {field: {from, to}}}. */
  details: Record<string, unknown>;
  created_at: string;
}

export interface ProjectEventsPage {
  data: ProjectEvent[];
  cursor: number;
  has_more: boolean;
}

export interface ProjectEventFilters {
  /** Event-type prefix: "file." filters the category, "file.uploaded" the exact type. */
  type?: string;
  /** Only events by this user. */
  actorId?: number;
}

const PAGE_SIZE = 50;

export function listProjectEvents(
  projectId: number,
  cursor = 0,
  filters: ProjectEventFilters = {},
): Promise<ProjectEventsPage> {
  const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
  if (cursor > 0) params.set('cursor', String(cursor));
  if (filters.type) params.set('type', filters.type);
  if (filters.actorId) params.set('actor_id', String(filters.actorId));
  return request<ProjectEventsPage>(`/projects/${projectId}/events?${params}`);
}
