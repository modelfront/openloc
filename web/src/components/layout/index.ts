/**
 * Barrel export for shared layout primitives.
 *
 * Lets callers do
 *   `import { PageHeader, EmptyState, LoadingPlaceholder } from '@/components/layout'`
 * instead of three separate import lines per page.
 *
 * Closes finding F.4 of the 2026-04-09 frontend DRY audit.
 */
export { PageHeader } from './PageHeader'
export { EmptyState } from './EmptyState'
export { LoadingPlaceholder } from './LoadingPlaceholder'
export { SlimCollapsibleSection } from './SlimCollapsibleSection'
