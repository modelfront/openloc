// Where each project asset lives, for links from the settings that use it.
// TMs, term bases, TM profiles, segmentation and filter profiles have their
// own pages.
// QA profiles and MT routers are rows on a list page: the link opens the list
// pointing at the row (`attentionHref`), and the row declares the same key.
// A workflow opens in the library's edit dialog (`?edit=<id>`).
import { attentionHref } from '@/lib/attention'
import { SEGMENTATION_PROFILES_PATH } from '@/pages/segmentation/segmentation-paths'
import { FILTER_PROFILES_PATH } from '@/pages/filter-profiles/filter-profile-paths'

export const tmPath = (id: number) => `/memories/${id}`
export const tmProfilePath = (id: number) => `/memories/profiles/${id}`
export const termBasePath = (id: number) => `/terminology/${id}`
export const segmentationProfilePath = (id: number) => `${SEGMENTATION_PROFILES_PATH}/${id}`
export const filterProfilePath = (id: number) => `${FILTER_PROFILES_PATH}/${id}`
export const workflowPath = (id: number) => `/workflows?edit=${id}`

/** Attention key of a QA profile's row on the QA profiles page. */
export const qaProfileKey = (id: number) => `qa-profile-${id}`
export const qaProfilePath = (id: number) => attentionHref('/quality', qaProfileKey(id))

/** The MT router card's attention key is its test id, `<testIdPrefix>-<id>`
 *  with the `mt-router` prefix set in MTRoutersSection. */
export const mtRouterPath = (id: number) => attentionHref('/mt', `mt-router-${id}`)
