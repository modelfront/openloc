import { attentionHref } from '@/lib/attention'

// The Organization page's tabs, one route each: the tab row, the redirects and
// the account menu link here. Each `<Route path>` in App.tsx stays a string
// literal, because the assistant's page-registry tests read the routes as text.
export const ORG_MEMBERS_PATH = '/org'
export const ORG_SETTINGS_PATH = '/org/settings'

/** Attention key of the Members tab's Vendors section. */
export const ORG_VENDORS_KEY = 'org-vendors'
/** The Members tab, pointing at its Vendors section: the bell's vendor notices open it. */
export const ORG_VENDORS_PATH = attentionHref(ORG_MEMBERS_PATH, ORG_VENDORS_KEY)
