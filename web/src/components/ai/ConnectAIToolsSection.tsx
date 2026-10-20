import { Copy, ExternalLink } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { TextLink } from '@/components/ui/text-link'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { SectionInfo } from '@/components/ui/section-info'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { siteLink, useAppConfig } from '@/pages/docs/DocsHost'

// Connecting an AI tool by sign-in: the user pastes the MCP address into the
// tool's own "add connector" page, and the tool sends them to the OAuth consent
// screen. An MCP client with OAuth and an https or loopback redirect works;
// Claude and ChatGPT get direct links
// because their add page is hard to find. No key changes hands, which is why
// this comes before access keys.
const TOOLS = [
  {
    id: 'claude',
    name: 'Claude',
    href: 'https://claude.ai/customize/connectors?modal=add-custom-connector',
    open: 'Open Claude connectors',
    steps: 'Paste the address as the server URL, click Add, then Connect, and approve in OpenLoc. Works in Claude Desktop too.',
  },
  {
    id: 'chatgpt',
    name: 'ChatGPT',
    href: 'https://chatgpt.com/plugins',
    open: 'Open ChatGPT plugins',
    steps: 'Click +, then Add custom MCP server. Paste the address as the server URL, choose OAuth, create and install it, and approve in OpenLoc.',
  },
] as const

export function ConnectAIToolsSection() {
  const { data: config } = useAppConfig()
  const mcpUrl = `${(config?.app_url || window.location.origin).replace(/\/+$/, '')}/mcp`

  return (
    <SlimCollapsibleSection
      title="Connect an AI tool"
      data-testid="connect-ai-tools-section"
      info={
        <SectionInfo label="About connecting an AI tool">
          Use OpenLoc from an AI tool that supports MCP, such as Claude or ChatGPT. The tool signs in as you; nothing to copy but the address.
        </SectionInfo>
      }
    >
      <div className="space-y-4">
        <p className="text-sm text-gray-600 dark:text-gray-300" data-testid="connect-any-tool">
          Most AI tools that support MCP can connect. In the tool, add a custom MCP server or connector, paste this
          address, choose OAuth or sign-in if it asks how to authenticate, and approve in OpenLoc.
        </p>
        <FormField label="MCP server address">
          <Input
            readOnly
            value={mcpUrl}
            clearable={false}
            onFocus={e => e.currentTarget.select()}
            data-testid="mcp-server-url"
            action={
              <CopyLinkButton
                url={mcpUrl}
                iconOnly
                icon={Copy}
                label="Copy address"
                toastMessage="Address copied"
                errorMessage="Could not copy — select the address and copy it manually"
                data-testid="copy-mcp-url"
              />
            }
          />
        </FormField>

        <p className="text-xs font-medium text-gray-500 dark:text-gray-400">Direct links for two common tools</p>
        <ul className="-mt-2 divide-y divide-gray-200 dark:divide-gray-800">
          {TOOLS.map(tool => (
            <li key={tool.id} className="flex flex-wrap items-center justify-between gap-3 py-3" data-testid={`connect-${tool.id}`}>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{tool.name}</p>
                <p className="text-xs text-gray-500 dark:text-gray-400">{tool.steps}</p>
              </div>
              <Button
                variant="outline"
                size="sm"
                nativeButton={false}
                // It navigates: a link to assistive tech, not Base UI's default button.
                role="link"
                render={<a href={tool.href} target="_blank" rel="noopener noreferrer" />}
              >
                {tool.open} <ExternalLink className="size-3.5" aria-hidden />
              </Button>
            </li>
          ))}
        </ul>

        <p className="text-xs text-gray-500 dark:text-gray-400">
          Commands for Claude Code and Codex, and more help: see{' '}
          <TextLink href={siteLink(config, '/docs/guides/mcp')} target="_blank" rel="noopener noreferrer">
            Connect external AI tools
          </TextLink>
          . A tool that cannot sign in uses an access key instead.
        </p>
      </div>
    </SlimCollapsibleSection>
  )
}
