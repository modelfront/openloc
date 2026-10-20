import { useTheme } from 'next-themes'
import { Monitor, Moon, Sun } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { headerControlHover } from './header-styles'

// The header's theme button: each click cycles light → dark → system. Used by
// the app header and the docs header.
export function ThemeToggle() {
  const { theme, setTheme } = useTheme()

  const cycle = () => {
    if (theme === 'light') setTheme('dark')
    else if (theme === 'dark') setTheme('system')
    else setTheme('light')
  }

  const icon = theme === 'dark'
    ? <Moon className="w-4 h-4" />
    : theme === 'light'
      ? <Sun className="w-4 h-4" />
      : <Monitor className="w-4 h-4" />
  const label = theme === 'dark' ? 'Dark' : theme === 'light' ? 'Light' : 'System'

  return (
    <Button
      variant="ghost"
      size="icon-xs"
      onClick={cycle}
      className={`text-gray-500 dark:text-gray-400 ${headerControlHover}`}
      title={`Theme: ${label}`}
    >
      {icon}
    </Button>
  )
}
