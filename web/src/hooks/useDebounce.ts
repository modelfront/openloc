import { useEffect, useState } from 'react'

/**
 * Returns a debounced copy of `value` that only updates `delay` ms after the
 * most recent change. Use it to keep a search input responsive while throttling
 * downstream queries.
 *
 * Example:
 *   const [search, setSearch] = useState('')
 *   const debouncedSearch = useDebounce(search, 300)
 *   useInfiniteQuery({ queryKey: ['x', debouncedSearch], ... })
 */
export function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value)

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])

  return debounced
}
