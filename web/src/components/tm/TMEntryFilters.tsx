import { Search } from 'lucide-react'
import type { TMLanguagePair } from '@/api'
import { Button } from '@/components/ui/button'
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
 * The TM browser's filters on one row: target-language tabs, the search
 * field, and the search options, which wrap to the next line as a group when
 * the row runs out of room. The source language is shown once above the list,
 * so the tabs only need the target language.
 */
export function TMEntryFilters({ langPairs, selectedPair, onSelectPair, search, serverSearchError }: Props) {
  const searchError = search.draftInvalid ? 'Invalid regular expression' : serverSearchError

  return (
    <div className="mb-4 flex items-center gap-x-3 gap-y-2 flex-wrap">
      <div className="flex gap-1 flex-wrap">
        <Button
          variant={selectedPair === 'all' ? 'default' : 'outline'}
          size="sm"
          onClick={() => onSelectPair('all')}
        >
          All
        </Button>
        {langPairs?.map(pair => {
          const key = encodeLangPair(pair.source_language, pair.target_language)
          return (
            <Button
              key={key}
              variant={selectedPair === key ? 'default' : 'outline'}
              size="sm"
              onClick={() => onSelectPair(key)}
            >
              <LanguageCode code={pair.target_language} />
            </Button>
          )
        })}
      </div>
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
