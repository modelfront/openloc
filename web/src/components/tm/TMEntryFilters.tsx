import { Search } from 'lucide-react'
import type { TMLanguagePair } from '@/api'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { Input } from '@/components/ui/input'
import { LanguageCode } from '@/components/ui/language-code'
import { SearchOptionsFields } from '@/components/SearchOptions'
import type { TMEntrySearch } from '@/hooks/useTMEntrySearch'
import { encodeLangPair } from '@/lib/lang-pair'

const MAX_SEARCH_LENGTH = 1000

interface Props {
  langPairs?: TMLanguagePair[]
  /** 'all' or an encodeLangPair key. */
  selectedPair: string
  onSelectPair: (key: string) => void
  search: TMEntrySearch
  /** The server's message for a search it refused. */
  serverSearchError: string | null
}

/**
 * The TM browser's filters on one row: the target-language choice, the search
 * field, and the search options, which wrap to the next line as a group when
 * the row runs out of room. The source language is shown once above the list,
 * so the choice only needs the target language.
 */
export function TMEntryFilters({ langPairs, selectedPair, onSelectPair, search, serverSearchError }: Props) {
  const searchError = search.draftInvalid ? 'Invalid regular expression' : serverSearchError

  return (
    <div className="mb-4 flex items-center gap-x-3 gap-y-2 flex-wrap">
      <ChoiceToggle
        size="sm"
        aria-label="Target language"
        value={selectedPair}
        onChange={onSelectPair}
        options={[
          { value: 'all', label: 'All' },
          ...(langPairs ?? []).map(pair => ({
            value: encodeLangPair(pair.source_language, pair.target_language),
            label: <LanguageCode code={pair.target_language} />,
          })),
        ]}
        // A TM can hold dozens of target languages: the choice scrolls inside
        // its row instead of running past it. `p-1` keeps the focus ring and
        // the checked segment's shadow inside the scroll box.
        className="max-w-full overflow-x-auto p-1"
        data-testid="tm-pair-filter"
      />
      {/* A fixed basis, not flex-1: the field holds 18rem, so a narrow row
          moves the options to the next line instead of squeezing the field. */}
      <div className="basis-72 grow max-w-md">
        <Input
          placeholder="Search entries..."
          value={search.text}
          onChange={e => search.setText(e.target.value)}
          className="h-8 text-sm"
          data-testid="tm-search"
          // The server refuses longer searches.
          maxLength={MAX_SEARCH_LENGTH}
          icon={<Search className="size-3.5" />}
        />
      </div>
      <SearchOptionsFields
        options={search.options}
        onChange={patch => search.setOptions(o => ({ ...o, ...patch }))}
        targetLabel="Target"
        withExactMatch
        regexError={searchError}
        regexHelp="tm"
        testIdPrefix="tm-search"
      />
    </div>
  )
}
