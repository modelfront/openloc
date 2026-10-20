import { Chip } from '@/components/ui/chip'
import { ProviderIcon } from './ProviderIcon'
import { vendorName } from './vendor-catalog'

// The shared prompt/engine summary chip: `<icon> Provider model | MODE`.
//
// The provider is always named, because that is the part a reader needs to tell
// two systems apart — a bare model string ("gpt-4o") says nothing about who
// runs it, and a provider with no model of its own (an extension) would
// otherwise have nothing to show but its mode. The model is secondary detail
// and is omitted when the provider has none.
//
// `model` and `mode` are BOTH optional, and each drops its separator with it.
// An MT engine has neither: it is one provider called one way, so the chip is
// just the badge and the vendor's name — which is the whole point there, since
// its own name is free text and need not mention the provider at all.
export function ProviderModelChip({
  provider, name: nameOverride, model, mode,
}: {
  provider?: string | null
  /** The provider's display name when the catalog doesn't know it (an extension's manifest name). */
  name?: string | null
  model?: string | null
  mode?: string
}) {
  const name = nameOverride || (provider ? vendorName(provider) : null)
  return (
    // ProviderIcon renders a monogram <span>, not an SVG, so it sizes itself
    // — Chip's [&_svg] rule can't reach it.
    <Chip icon={provider ? <ProviderIcon provider={provider} className="mr-0.5 size-4" /> : undefined}>
      {name && <span className="font-medium">{name}</span>}
      {model && <span className="font-normal opacity-70">{model}</span>}
      {mode && (name || model) && <span className="opacity-40">|</span>}
      {mode}
    </Chip>
  )
}
