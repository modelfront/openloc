import { useQuery } from '@tanstack/react-query'
import { getMe } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { AIVendorsSection } from '../components/ai/AIVendorsSection'
import { APITokensSection } from '../components/ai/APITokensSection'
import { ConnectAIToolsSection } from '../components/ai/ConnectAIToolsSection'

/**
 * Everything that can talk to this org's AI surface, in one place:
 *
 * - **Providers** — the org's provider credentials (OpenAI / Claude / custom
 *   providers) that AI engines run with. Managers see them read-only;
 *   owner/admin edit keys and add, replace or remove custom providers,
 *   mirroring the backend's RequireRole on every write.
 * - **Connect an AI tool** — the MCP address and where to paste it in Claude
 *   and ChatGPT, which then sign in through the OAuth consent screen.
 * - **Access keys** — the user's own personal access tokens, for tools that
 *   cannot sign in. Both are per-user and open to EVERY role, which is why
 *   the page itself is not admin-gated: a linguist connects their own tool to
 *   drive the CAT-editor tools from an agent.
 *
 * The split lives here rather than on the route so a linguist landing on the
 * page gets their own sections instead of a redirect.
 */
export default function AISettingsPage() {
  const { data: me } = useQuery({ queryKey: queryKeys.me.all, queryFn: getMe })
  const canManageVendors = isAdminRole(me?.role)
  const canSeeVendors = isManagerRole(me?.role)

  return (
    <PageContainer>
      <PageHeader title="AI settings" />
      {canSeeVendors && (
        <div data-guide="ai_settings.providers">
          <AIVendorsSection defaultOpen canWrite={canManageVendors} />
        </div>
      )}
      <div data-guide="ai_settings.connect">
        <ConnectAIToolsSection />
      </div>
      <div data-guide="ai_settings.tokens">
        <APITokensSection />
      </div>
    </PageContainer>
  )
}
