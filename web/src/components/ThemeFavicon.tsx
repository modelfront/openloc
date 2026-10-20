import { useEffect } from 'react'
import { useTheme } from 'next-themes'

/** Follow the app's resolved theme, including an explicit override of the OS. */
export function ThemeFavicon() {
  const { resolvedTheme } = useTheme()
  useEffect(() => {
    if (!resolvedTheme) return
    const icon = document.querySelector<HTMLLinkElement>('link[data-theme-favicon]')
    if (icon) icon.href = resolvedTheme === 'dark' ? '/favicon-dark.svg' : '/favicon.svg'
  }, [resolvedTheme])
  return null
}
