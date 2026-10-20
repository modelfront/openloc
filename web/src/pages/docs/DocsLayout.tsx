import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { PageContainer } from '@/components/layout/PageContainer'
import { ThemeToggle } from '@/components/layout/ThemeToggle'
import { Wordmark } from '@/components/layout/Wordmark'
import { PaletteProvider } from '@/components/PaletteProvider'
import { useAppConfig } from './DocsHost'

/**
 * The page frame of the docs and the API docs: a slim header over the content,
 * without the app's nav rail. Signed in on the app's address, it leads back
 * into the app; signed out, and always on the docs host, "Open app" opens the
 * app on its own address.
 */
export function DocsLayout({ signedIn, children }: { signedIn: boolean; children: ReactNode }) {
  return (
    <div className="h-screen flex flex-col bg-background overflow-hidden">
      {/* Signed out there is no saved appearance to fetch; the palette still
          follows the theme button. */}
      <PaletteProvider anonymous={!signedIn} />
      <header className="h-12 shrink-0 border-b border-gray-200 dark:border-gray-800 bg-card">
        <PageContainer width="docsBar" className="flex h-full items-center justify-between">
          <a href="https://openloc.com" target="_blank" rel="noopener noreferrer" className="hover:underline"><Wordmark /></a>
          <div className="flex items-center gap-2">
            <ThemeToggle />
            {signedIn ? <BackToApp /> : <OpenApp />}
          </div>
        </PageContainer>
      </header>
      <main className="flex-1 overflow-y-auto pt-6">{children}</main>
    </div>
  )
}

function BackToApp() {
  return (
    <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/" />}>
      Back to app
    </Button>
  )
}

// The app's home on its own address: someone signed in to the app lands in
// it, someone signed out is asked to sign in. The docs host cannot tell which
// (the session cookie belongs to the app's address), hence one button for both.
function OpenApp() {
  const { data: config } = useAppConfig()
  const href = config?.app_url ? `${config.app_url.replace(/\/+$/, '')}/` : '/'
  return (
    <Button variant="outline" size="sm" nativeButton={false} render={<a href={href} target="_blank" rel="noopener noreferrer" />}>
      Open app
    </Button>
  )
}
