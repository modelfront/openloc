// Shared MT-engine helpers (AI-as-MT). An MT route can point at a
// classic MT engine (kind 'mt') or an AI engine of type 'mt' (kind 'ai'); the
// two id-spaces overlap, so the `kind` discriminator travels with every entry.

type ClassicEngine = { id: number; name: string; unusable?: string }
// Only AI engines created with the 'mt' purpose can translate
// (QE+APE-purpose engines belong on workflow steps).
type AIEngineLike = { id: number; name: string; purpose: string; unusable?: string }

export type CombinedEngine = { id: number; name: string; kind: string; unusable: string }

/**
 * Combined picker list: classic engines (kind 'mt') + mt-purpose AI engines
 * (kind 'ai', suffixed "(AI)" so the two are visually distinct).
 *
 * Unusable engines are REPORTED, not filtered, so a rule
 * that still points at a switched-off engine keeps a visible option. The caller
 * hides them from NEW picks and keeps the one a rule already holds, flagged.
 */
export function combineMTEngines(
  mtEngines: ClassicEngine[] | undefined,
  aiEngines: AIEngineLike[] | undefined,
): CombinedEngine[] {
  return [
    ...(mtEngines ?? []).map(e => ({ id: e.id, name: e.name, kind: 'mt', unusable: e.unusable ?? '' })),
    ...(aiEngines ?? []).filter(e => e.purpose === 'mt')
      .map(e => ({ id: e.id, name: `${e.name} (AI)`, kind: 'ai', unusable: e.unusable ?? '' })),
  ]
}
