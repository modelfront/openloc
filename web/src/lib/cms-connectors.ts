/**
 * CMS connector catalogue for the Integrations page.
 *
 * Static catalogue; no backend yet — there is no `cms_connectors` table, no
 * handler and no sync. The shape is close to what a catalogue endpoint would
 * return (`GET /api/integrations/cms`), so a backend swaps the data source
 * rather than rewriting the page.
 *
 * The entries are a judgement call, not a researched market ranking.
 *
 * Brand marks live in the page (IntegrationsPage), not here: five come from
 * `simple-icons`, and Adobe's is inlined because that registry no longer
 * carries it.
 *
 * Deliberately no credential fields: real auth here is mostly OAuth redirect
 * flows (Adobe IMS for AEM/Commerce/Marketo, OAuth2 for Sitecore XM Cloud and
 * Drupal), not API-key forms, so inventing field lists would encode something
 * false. Connection setup is left for when the backend defines it.
 */

export type ConnectorCategory =
  | 'Enterprise DXP'
  | 'Headless CMS'
  | 'Traditional CMS'
  | 'Commerce'
  | 'Marketing'
  | 'Code & docs'

export type ConnectorStatus = 'connected' | 'disconnected'

export interface CmsConnector {
  id: string
  name: string
  category: ConnectorCategory
  status: ConnectorStatus
}

export const CMS_CONNECTORS: CmsConnector[] = [
  {
    id: 'aem',
    name: 'Adobe Experience Manager',
    category: 'Enterprise DXP',
    status: 'disconnected',
  },
  {
    id: 'adobe-commerce',
    name: 'Adobe Commerce',
    category: 'Commerce',
    status: 'disconnected',
  },
  {
    id: 'marketo',
    name: 'Adobe Marketo Engage',
    category: 'Marketing',
    status: 'disconnected',
  },
  {
    id: 'wordpress',
    name: 'WordPress',
    category: 'Traditional CMS',
    status: 'disconnected',
  },
  {
    id: 'contentful',
    name: 'Contentful',
    category: 'Headless CMS',
    status: 'disconnected',
  },
  {
    id: 'contentstack',
    name: 'Contentstack',
    category: 'Headless CMS',
    status: 'disconnected',
  },
  {
    id: 'sitecore',
    name: 'Sitecore',
    category: 'Enterprise DXP',
    status: 'disconnected',
  },
  {
    id: 'drupal',
    name: 'Drupal',
    category: 'Traditional CMS',
    status: 'disconnected',
  },
  {
    id: 'github',
    name: 'GitHub',
    category: 'Code & docs',
    status: 'disconnected',
  },
]
