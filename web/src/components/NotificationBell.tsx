import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getUnreadCount, listNotifications, markNotificationRead, markAllNotificationsRead, type Notification } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { useNotificationStream } from '../hooks/useNotificationStream'
import { headerControlHover } from './layout/header-styles'
import { timeAgo } from '../lib/time-ago'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from './ui/tooltip'
import { TextLink } from '@/components/ui/text-link'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Badge } from '@/components/ui/badge'
import { Bell } from 'lucide-react'

const EVENT_ICONS: Record<string, string> = {
  assignment: '📋',
  rejection: '↩️',
  completion: '✅',
  deadline: '⏰',
  mt_failed: '🌐',
  job_failed: '⚠️',
  issue_updated: '💬',
  issue_resolved: '✔️',
}

/** The notification's text, cut at two lines; hover shows the rest. */
export function NotificationPreview({ text }: { text: string }) {
  const ref = useRef<HTMLParagraphElement>(null)
  const [clipped, setClipped] = useState(false)
  const measure = () => {
    const el = ref.current
    setClipped(!!el && el.scrollHeight > el.clientHeight)
  }
  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger
          render={
            <p
              ref={ref}
              className="text-xs text-gray-500 dark:text-gray-500 mt-0.5 line-clamp-2"
              onMouseEnter={measure}
              data-testid="notification-preview"
            />
          }
        >
          {text}
        </TooltipTrigger>
        {clipped && (
          <TooltipContent side="left" className="max-w-sm whitespace-pre-wrap break-words" data-testid="notification-preview-full">
            {text}
          </TooltipContent>
        )}
      </Tooltip>
    </TooltipProvider>
  )
}

export default function NotificationBell() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()

  // Live SSE delivery + cross-tab BroadcastChannel. SSE invalidates the count
  // query the instant a notification lands; the 60s poll below is the
  // fallback.
  useNotificationStream()

  const { data: count = 0 } = useQuery({
    queryKey: queryKeys.notifications.count,
    queryFn: getUnreadCount,
    refetchInterval: 60000, // 60s polling fallback when SSE is down
  })

  const { data: notifs } = useQuery({
    queryKey: queryKeys.notifications.all,
    queryFn: () => listNotifications({ limit: 10 }),
    enabled: open,
  })

  const notificationKeys = [queryKeys.notifications.count, queryKeys.notifications.all]
  const markReadMut = useMutationWithToast({
    mutationFn: markNotificationRead,
    successMessage: () => null,
    errorMessage: 'Failed to mark as read',
    invalidateKeys: notificationKeys,
  })

  const markAllMut = useMutationWithToast({
    mutationFn: markAllNotificationsRead,
    successMessage: () => null,
    errorMessage: 'Failed to mark all as read',
    invalidateKeys: notificationKeys,
  })

  const handleClick = (n: Notification) => {
    if (!n.read) markReadMut.mutate(n.id)
    setOpen(false)
    if (n.reference_type === 'issue' && n.reference_id > 0) {
      navigate(`/issues/${n.reference_id}`)
      return
    }
    if (n.reference_type === 'job' && n.reference_id > 0) {
      // Navigate to the job — we'd need the project ID, but for now just close
      // TODO: include project_id in notification for direct navigation
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={`relative p-1.5 text-gray-500 dark:text-gray-400 ${headerControlHover}`}
        title="Notifications"
      >
        <Bell className="size-4.5" />
        {count > 0 && (
          <Badge variant="count" className="absolute -top-0.5 -right-0.5">
            {count > 99 ? '99+' : count}
          </Badge>
        )}
      </PopoverTrigger>

      <PopoverContent align="end" className="w-80 gap-0 overflow-hidden p-0" data-testid="notification-menu">
        <div className="flex items-center justify-between px-3 py-2 border-b border-gray-100 dark:border-gray-800">
          <span className="text-sm font-semibold text-gray-700 dark:text-gray-200">Notifications</span>
          {count > 0 && (
            <TextLink className="text-xs" onClick={() => markAllMut.mutate()}>
              Mark all read
            </TextLink>
          )}
        </div>

        <div className="max-h-80 overflow-y-auto">
          {!notifs?.data?.length ? (
            <p className="text-sm text-gray-400 dark:text-gray-500 text-center py-6">No notifications</p>
          ) : (
            notifs.data.map(n => (
              // eslint-disable-next-line no-restricted-syntax -- notification row target, not an action button
              <button
                key={n.id}
                onClick={() => handleClick(n)}
                className={`w-full text-left px-3 py-2.5 border-b border-gray-50 dark:border-gray-800 last:border-b-0 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors ${
                  !n.read ? 'bg-primary/10' : ''
                }`}
              >
                <div className="flex items-start gap-2">
                  <span className="text-sm mt-0.5">{EVENT_ICONS[n.event_type] ?? '📌'}</span>
                  <div className="flex-1 min-w-0">
                    <p className={`text-sm leading-snug ${!n.read ? 'font-medium text-gray-900 dark:text-gray-100' : 'text-gray-600 dark:text-gray-400'}`}>
                      {n.subject}
                    </p>
                    {n.body_preview && <NotificationPreview text={n.body_preview} />}
                    <p className="text-[10px] text-gray-400 dark:text-gray-600 mt-1">{timeAgo(n.created_at)}</p>
                  </div>
                  {!n.read && (
                    <span className="w-2 h-2 rounded-full bg-primary mt-1.5 flex-shrink-0" />
                  )}
                </div>
              </button>
            ))
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
