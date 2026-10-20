import { useQuery } from '@tanstack/react-query'
import { getMe } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isAdminRole } from '../lib/roles'

/** Write actions on every AI tab are owner/admin-only; the backend enforces
 *  this too. Project managers get a read-only view. */
export function useCanWriteAI() {
  const { data: me } = useQuery({ queryKey: queryKeys.me.all, queryFn: getMe })
  return isAdminRole(me?.role)
}
