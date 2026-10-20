import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'

/**
 * A project without files has no Tasks tab to show, so landing on Tasks
 * opens Files instead. It checks where the browser is now, not the render
 * that scheduled it: a tab click moves the browser first, and the redirect
 * must not undo it.
 */
export function useOpenFilesWhenEmpty({ pid, noFiles, isDraft, activeTab, pathname, search }: {
  pid: number
  noFiles: boolean
  isDraft: boolean
  activeTab: string
  /** The router's path on this render: the redirect runs only while the browser is still there. */
  pathname: string
  search: string
}) {
  const navigate = useNavigate()
  useEffect(() => {
    if (noFiles && !isDraft && activeTab === 'tasks' && window.location.pathname === pathname) {
      navigate(`/projects/${pid}/files${search}`, { replace: true })
    }
  }, [noFiles, isDraft, activeTab, pid, pathname, search, navigate])
}
