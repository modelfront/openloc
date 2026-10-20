import type { EnginePurpose } from '../../api'
import { flagsToMode, type OutputFlags } from './output-mode'

/**
 * Where a prompt of this purpose lives.
 *
 * MT prompts and reviewing prompts are the same entity with different output
 * flags, but they sit under different sections — so the list, the editor and
 * every link between them have to agree on one base path per purpose. Derived
 * from the purpose rather than passed down, because a wrong answer sends
 * someone to a page whose list filters their prompt straight back out.
 */
export function promptsBasePath(purpose: EnginePurpose): string {
  return purpose === 'mt' ? '/mt/prompts' : '/ai/prompts'
}

/**
 * The page for one prompt, chosen by the prompt's OWN output flags rather than
 * by whatever is showing it. A prompt's purpose is implicit in those flags
 * (flagsToMode), so they are what decides which section lists it — and a link
 * built from anything else can land on a page whose list filters the prompt
 * straight back out.
 */
export function promptPath(prompt: OutputFlags & { id: number }): string {
  const purpose: EnginePurpose = flagsToMode(prompt) === 'mt' ? 'mt' : 'qe_ape'
  return `${promptsBasePath(purpose)}/${prompt.id}`
}
