/// <reference types="vitest/config" />
import path from 'path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import honeybadgerRollupPlugin from '@honeybadger-io/rollup-plugin'
import type { Rollup } from 'vite'

// Sourcemap upload to Honeybadger, active only inside the Docker image build
// (the image build passes both as --build-arg; local/CI builds have neither and
// skip the upload). VERSION is the git hash — it must equal the revision the
// backend serves via /api/config, or Honeybadger won't match the maps.
const hbUploadKey = process.env.HONEYBADGER_API_KEY
const hbRevision = process.env.VERSION

// The upload must never block a deploy: the plugin THROWS when uploads fail
// (after its 3 retries), which would fail `vite build` → the Docker build →
// the release, during any Honeybadger outage. Sourcemaps are a nice-to-have;
// shipping is not. Wrap its writeBundle hook to log loudly and continue.
function failOpen(plugin: Rollup.Plugin): Rollup.Plugin {
  const original = plugin.writeBundle
  if (typeof original !== 'function') return plugin
  return {
    ...plugin,
    writeBundle: async function (this: unknown, ...args: unknown[]) {
      try {
        await (original as (...a: unknown[]) => Promise<unknown>).apply(this, args)
      } catch (err) {
        console.error('[honeybadger] sourcemap upload FAILED — build continues WITHOUT readable stack traces for this revision:', err)
      }
    },
  } as Rollup.Plugin
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
    // Force a single React instance across all pre-bundled deps. Guards against
    // "Invalid hook call / more than one copy of React" when a base-ui subpath
    // gets optimized into its own React copy.
    dedupe: ['react', 'react-dom'],
  },
  optimizeDeps: {
    // Pre-bundle base-ui subpaths at cold start. Without this, adding a new
    // subpath import (e.g. @base-ui/react/menu for DropdownMenu) to an already
    // running dev server makes Vite lazily re-optimize and duplicate React,
    // breaking every base-ui component until the cache is rebuilt. Listing them
    // here bundles them with the shared React from the first optimize pass.
    include: ['@base-ui/react/menu', '@base-ui/react/popover', '@base-ui/react/tooltip'],
  },
  build: {
    // 'hidden' generates .map files without the sourceMappingURL comment.
    // They're uploaded to Honeybadger (below) and then DELETED from the
    // Docker image (see dockerfiles/openloc.dockerfile) — they must never
    // be served publicly.
    sourcemap: 'hidden',
    rollupOptions: {
      // Inside build.rollupOptions.plugins, NOT the top-level plugins array:
      // top-level would run (and upload) on `vite serve` too.
      plugins:
        hbUploadKey && hbRevision
          ? [
              failOpen(
                honeybadgerRollupPlugin({
                  apiKey: hbUploadKey,
                  // Wildcard host: the deploy origin isn't known at build time;
                  // Honeybadger matches minified URLs with * wildcards.
                  assetsUrl: 'http*://*/assets',
                  revision: hbRevision,
                  // Plugin README recommends a lower worker count in Docker.
                  workerCount: 3,
                }),
              ),
            ]
          : [],
      output: {
        manualChunks: {
          // Vendor: React ecosystem (rarely changes → long cache)
          'vendor-react': ['react', 'react-dom', 'react-router-dom', '@tanstack/react-query'],
          // TipTap + ProseMirror (loaded only when CAT editor is opened)
          'vendor-tiptap': [
            '@tiptap/react', '@tiptap/core', '@tiptap/starter-kit',
            '@tiptap/extension-placeholder', '@tiptap/extension-superscript',
            '@tiptap/extension-subscript',
          ],
        },
      },
    },
  },
  server: {
    // Dev "instance" ports come from the environment (exported by the Makefile
    // from an optional, git-ignored instance.conf).
    // Falls back to the instance-1 (9000-block) ports so a checkout with no
    // instance.conf runs as the original. strictPort makes a collision fail
    // loudly instead of silently drifting onto another port.
    port: Number(process.env.FRONTEND_PORT ?? 9000),
    strictPort: true,
    // Dev server only — no effect on `vite build` output. Pre-transforms the app
    // entry at boot so the first page load doesn't pay for the whole module
    // graph, which otherwise exceeds the 10s E2E per-test timeout.
    warmup: {
      clientFiles: ['./src/main.tsx', './src/App.tsx'],
    },
    proxy: {
      // A regex so /api-docs (an SPA route) is not proxied with /api/*.
      '^/api/': {
        target: `http://localhost:${process.env.BACKEND_PORT ?? 9001}`,
        cookieDomainRewrite: 'localhost',
      },
      '/health': `http://localhost:${process.env.BACKEND_PORT ?? 9001}`,
      // OAuth authorization server: /oauth/authorize is a
      // BACKEND-rendered page and /.well-known/* is client discovery — both
      // must reach Go, not the SPA fallback. /mcp for parity with prod.
      '/oauth': {
        target: `http://localhost:${process.env.BACKEND_PORT ?? 9001}`,
        cookieDomainRewrite: 'localhost',
      },
      '/.well-known': `http://localhost:${process.env.BACKEND_PORT ?? 9001}`,
      '/mcp': `http://localhost:${process.env.BACKEND_PORT ?? 9001}`,
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
