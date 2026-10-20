import { Component, type ReactNode, type ErrorInfo } from 'react'
import { Button } from '@/components/ui/button'
import { notify } from '@/lib/errtrack'
import { isChunkLoadError } from '@/lib/lazy-reload'
import { ReportCrashButton } from '@/components/issues/ReportCrashButton'

interface Props {
  children: ReactNode
  /** Heading shown in the fallback panel, e.g. "CAT editor crashed". */
  title?: string
}

/**
 * Render-error boundary with a visible fallback + Retry, reporting to
 * Honeybadger (via lib/errtrack). Mounted at the app root so a crash never
 * shows a blank page, and again around the CAT editor so an editor crash
 * doesn't unmount the nav shell.
 */
export class ErrorBoundary extends Component<Props, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('ErrorBoundary caught:', error, info)
    // Stale-chunk failures are reported by lazy-reload's own path (with a
    // reload); reaching here means the reload budget is spent and this is a
    // genuinely broken build — still worth a notice, but under its own class
    // so it doesn't group with app render bugs.
    notify(error, {
      name: isChunkLoadError(error) ? 'ChunkLoadError' : 'ReactRenderError',
      context: {
        componentStack: info.componentStack,
        boundary: this.props.title ?? 'root',
      },
    })
  }
  render() {
    if (this.state.error)
      return (
        <div className="p-8 m-8 bg-red-50 dark:bg-red-950 border border-red-300 dark:border-red-700 rounded-lg">
          <h2 className="text-lg font-bold text-red-700 dark:text-red-300 mb-2">
            {this.props.title ?? 'Something went wrong'}
          </h2>
          <pre className="text-sm text-red-600 dark:text-red-400 whitespace-pre-wrap">{this.state.error.message}</pre>
          <pre className="text-xs text-red-500 dark:text-red-500 mt-2 whitespace-pre-wrap">{this.state.error.stack}</pre>
          <div className="mt-4 flex gap-2">
            <Button onClick={() => this.setState({ error: null })}>
              Retry
            </Button>
            <Optional><ReportCrashButton error={this.state.error} /></Optional>
          </div>
        </div>
      )
    return this.props.children
  }
}

/**
 * Renders nothing if its children throw. The crash screen must never crash
 * itself: the report button needs the app's providers, which a boundary high
 * enough up may not have.
 */
class Optional extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    return this.state.failed ? null : this.props.children
  }
}
