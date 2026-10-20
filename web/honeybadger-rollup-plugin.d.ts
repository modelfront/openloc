// @honeybadger-io/rollup-plugin ships no type declarations (CJS build only).
// Minimal surface we use in vite.config.ts; options per the plugin README.
declare module '@honeybadger-io/rollup-plugin' {
  // Vite bundles its own rollup; using vite's re-exported types keeps the
  // Plugin type identical to what build.rollupOptions.plugins expects
  // (the top-level `rollup` package is a different copy with drifted types).
  import type { Rollup } from 'vite'
  type Plugin = Rollup.Plugin
  interface HoneybadgerRollupPluginOptions {
    apiKey: string
    assetsUrl: string
    revision?: string
    endpoint?: string
    retries?: number
    workerCount?: number
    silent?: boolean
    ignorePatterns?: string[]
    deploy?: boolean | { repository?: string; localUsername?: string; environment?: string }
  }
  export default function honeybadgerRollupPlugin(options: HoneybadgerRollupPluginOptions): Plugin
}
