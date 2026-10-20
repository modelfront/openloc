import { useState } from 'react'
import { useQuery, type QueryKey } from '@tanstack/react-query'
import { useMutationWithToast } from './use-mutation-with-toast'

interface UseCRUDListOptions<TItem, TCreateVars> {
  queryKey: QueryKey
  queryFn: () => Promise<TItem[]>
  createFn: (vars: TCreateVars) => Promise<unknown>
  renameFn?: (params: { id: number; name: string }) => Promise<unknown>
  deleteFn: (id: number) => Promise<unknown>
  entityName: string // "TM", "Term base", "QA profile", etc.
  /** False when the create form renders the rejection inline itself. */
  createErrorToast?: boolean
  onCreateSuccess?: () => void
  onDeleteSuccess?: () => void
}

/**
 * Manages CRUD lifecycle state for resource list pages.
 *
 * Provides: query (with `loadFailed`/`error`/`refetch` for the LoadFailed
 * state), create/rename/delete mutations with toasts,
 * inline rename state, delete confirmation state, and form visibility toggle.
 *
 * Does NOT own the create form (schema/rendering vary per page).
 */
export function useCRUDList<TItem, TCreateVars = void>({
  queryKey,
  queryFn,
  createFn,
  renameFn,
  deleteFn,
  entityName,
  createErrorToast,
  onCreateSuccess,
  onDeleteSuccess,
}: UseCRUDListOptions<TItem, TCreateVars>) {
  const { data: items, isLoading, isError, error, refetch: refetchQuery } = useQuery({ queryKey, queryFn })
  // True only when there is nothing to show: a failed background refetch
  // keeps the cached rows on screen.
  const loadFailed = isError && items === undefined
  const refetch = () => { void refetchQuery() }

  const [showForm, setShowForm] = useState(false)
  const [renamingId, setRenamingId] = useState<number | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)

  const createMut = useMutationWithToast({
    mutationFn: createFn,
    successMessage: `${entityName} created`,
    errorMessage: 'Create failed',
    errorToast: createErrorToast,
    invalidateKeys: [queryKey],
    onSuccess: () => {
      setShowForm(false)
      onCreateSuccess?.()
    },
  })

  // Always call the hook (React rules) — use a no-op when rename isn't needed.
  const noopRename = () => Promise.resolve(undefined)
  const renameMut = useMutationWithToast({
    mutationFn: renameFn ?? noopRename,
    successMessage: `${entityName} renamed`,
    errorMessage: 'Rename failed',
    invalidateKeys: [queryKey],
    onSuccess: () => setRenamingId(null),
  })
  const hasRename = !!renameFn

  const deleteMut = useMutationWithToast({
    mutationFn: deleteFn,
    successMessage: `${entityName} deleted`,
    errorMessage: 'Delete failed',
    invalidateKeys: [queryKey],
    onSuccess: onDeleteSuccess,
  })

  const startRename = (e: React.MouseEvent, id: number, currentName: string) => {
    e.preventDefault()
    e.stopPropagation()
    setRenamingId(id)
    setRenameValue(currentName)
  }

  const submitRename = (id: number) => {
    const trimmed = renameValue.trim()
    if (trimmed && hasRename) {
      renameMut.mutate({ id, name: trimmed })
    } else {
      setRenamingId(null)
    }
  }

  const cancelRename = () => setRenamingId(null)

  const handleDelete = (e: React.MouseEvent, id: number, name: string) => {
    e.preventDefault()
    e.stopPropagation()
    setDeleteTarget({ id, name })
  }

  const confirmDelete = () => {
    if (deleteTarget) deleteMut.mutate(deleteTarget.id)
  }

  return {
    items,
    isLoading,
    loadFailed,
    error,
    refetch,

    showForm,
    setShowForm,
    createMut,

    renamingId,
    renameValue,
    setRenameValue,
    startRename,
    submitRename,
    cancelRename,
    renameMut,

    deleteTarget,
    setDeleteTarget,
    handleDelete,
    confirmDelete,
    deleteMut,
  }
}
