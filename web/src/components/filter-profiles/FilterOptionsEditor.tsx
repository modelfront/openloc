// The import options of one filter profile, by format. One case per format:
// a format that gains options gets a case here and nothing else changes.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listLanguages } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { langPickerProps, toLangOptions } from '@/components/lang-options'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Combobox, MultiCombobox, type ComboboxOption } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { TooltipProvider } from '@/components/ui/tooltip'
import { RowDeleteAction } from '@/components/list/RowAction'
import type { FilterFormat } from '@/lib/filter-formats'
import { columnNumber, type FilterOptionValues } from '@/lib/filter-options'

export interface FilterOptionErrors {
  sheets?: string
  columns?: string
  targetColumns?: string
  htmlAttributes?: string
  xmlInline?: string
  xmlNoTranslate?: string
  xmlRefElements?: string
  xmlRefAttributes?: string
  xmlSkip?: string
}

interface Props {
  format: FilterFormat
  value: FilterOptionValues
  onChange: (patch: Partial<FilterOptionValues>) => void
  readOnly: boolean
  errors?: FilterOptionErrors
}

export function FilterOptionsEditor({ format, value, onChange, readOnly, errors }: Props) {
  switch (format) {
    case 'json_i18n':
    case 'po':
    case 'txt':
      return <InlineHTMLField value={value} onChange={onChange} readOnly={readOnly} />
    case 'xlsx':
      return (
        <div className="grid gap-4">
          <InlineHTMLField value={value} onChange={onChange} readOnly={readOnly} />
          <XLSXRulesFields value={value} onChange={onChange} readOnly={readOnly} errors={errors} />
        </div>
      )
    case 'html':
      return <HTMLFields value={value} onChange={onChange} readOnly={readOnly} errors={errors} />
    case 'xml':
      return <XMLFields value={value} onChange={onChange} readOnly={readOnly} errors={errors} />
  }
}

type FieldProps = Omit<Props, 'format'>

function InlineHTMLField({ value, onChange, readOnly }: FieldProps) {
  return (
    <CheckboxField
      checked={value.inlineHTML}
      onChange={inlineHTML => onChange({ inlineHTML })}
      disabled={readOnly}
      data-testid="filter-inline-html"
      label="Strings contain HTML"
      description="Tags in the strings, such as <b>, become protected tags. Only tags: HTML profile options such as attributes do not apply here. Turn off for plain text."
    />
  )
}

/** A–Z to pick from; any other letter is typed in. */
const BASE_COLUMNS = Array.from({ length: 26 }, (_, i) => String.fromCharCode(65 + i))

function XLSXRulesFields({ value, onChange, readOnly, errors }: FieldProps) {
  const columnOptions: ComboboxOption[] = useMemo(() => {
    const letters = [...new Set([...BASE_COLUMNS, ...value.columns])]
    // Typed values that are not letters sort last, where their error points.
    letters.sort((a, b) => (columnNumber(a) || Infinity) - (columnNumber(b) || Infinity))
    return letters.map(l => ({ value: l, label: l }))
  }, [value.columns])
  const sheetOptions: ComboboxOption[] = value.sheets.map(s => ({ value: s, label: s }))
  const add = (list: string[], text: string, upper: boolean) => {
    const v = upper ? text.trim().toUpperCase() : text.trim()
    return v && !list.includes(v) ? [...list, v] : list
  }

  return (
    <div className="grid gap-4" data-testid="filter-xlsx-rules">
      <p className="text-xs text-muted-foreground">
        These rules choose what to translate in a workbook. With no sheets, no columns, no header row and no target
        columns, the upload asks which columns to translate. A workbook with language codes in row 1 is read by its languages and ignores
        these rules.
      </p>
      <FormField label="Sheets" error={errors?.sheets}>
        <div data-field="sheets">
          <MultiCombobox value={value.sheets} options={sheetOptions} wrap disabled={readOnly}
            placeholder="Every visible sheet" searchPlaceholder="Type a sheet name" createOptionLabel={'Add "{}"'}
            onCreateOption={text => onChange({ sheets: add(value.sheets, text, false) })}
            onChange={sheets => onChange({ sheets })} aria-label="Sheets" aria-invalid={!!errors?.sheets} data-testid="filter-xlsx-sheets" />
        </div>
      </FormField>
      <CheckboxField
        checked={value.headerRow}
        onChange={headerRow => onChange({ headerRow })}
        disabled={readOnly}
        data-testid="filter-xlsx-header-row"
        label="Row 1 is a header"
        description="Its cells are column titles, not text to translate."
      />
      <FormField label="Columns" error={errors?.columns}>
        <div data-field="columns">
          <MultiCombobox value={value.columns} options={columnOptions} wrap disabled={readOnly}
            placeholder="Every text cell" searchPlaceholder="Search or type a column letter" createOptionLabel={'Add "{}"'}
            onCreateOption={text => onChange({ columns: add(value.columns, text, true) })}
            onChange={columns => onChange({ columns })} aria-label="Columns" aria-invalid={!!errors?.columns} data-testid="filter-xlsx-columns" />
        </div>
      </FormField>
      <TargetColumnsFields value={value} onChange={onChange} readOnly={readOnly} errors={errors} />
    </div>
  )
}

/**
 * One row per target language: the column its translations go into, next to
 * the one source column. No rows: each translation replaces its source cell.
 */
function TargetColumnsFields({ value, onChange, readOnly, errors }: FieldProps) {
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const rows = value.targetColumns
  const setRow = (i: number, row: Partial<(typeof rows)[number]>) =>
    onChange({ targetColumns: rows.map((r, j) => (j === i ? { ...r, ...row } : r)) })
  // Each picker offers the languages no other row holds.
  const optionsFor = (own: string) =>
    toLangOptions((languages ?? []).filter(l => l.code === own || !rows.some(r => r.language === l.code)))
  return (
    <FormField label="Target columns" error={errors?.targetColumns}>
      <div data-field="targetColumns" className="grid gap-2" data-testid="filter-xlsx-targets">
        <p className="text-xs text-muted-foreground">
          For a multilingual workbook: each language's translation goes into its own column on the same row, and the
          source text stays. Choose one source column above. A language with no column here cannot be exported. With
          no languages, each translation replaces its source text.
        </p>
        <TooltipProvider delay={200}>
          {rows.map((row, i) => (
            <div key={i} className="grid grid-cols-[1fr_6rem_auto] items-start gap-2" data-testid={`filter-xlsx-target-${i}`}>
              <Combobox {...langPickerProps} value={row.language} options={optionsFor(row.language)} disabled={readOnly}
                placeholder="Language" aria-label={`Language of target column ${i + 1}`} data-testid={`filter-xlsx-target-language-${i}`}
                onChange={language => setRow(i, { language })} />
              <Input value={row.column} disabled={readOnly} placeholder="Column" clearable={false}
                aria-label={`Column for ${row.language || `target ${i + 1}`}`} data-testid={`filter-xlsx-target-column-${i}`}
                onChange={e => setRow(i, { column: e.target.value.toUpperCase() })} />
              {!readOnly && (
                <RowDeleteAction label={`Delete ${row.language || 'this language'}`} testId={`filter-xlsx-target-delete-${i}`}
                  onClick={() => onChange({ targetColumns: rows.filter((_, j) => j !== i) })} />
              )}
            </div>
          ))}
        </TooltipProvider>
        {!readOnly && (
          <div>
            <Button variant="outline" size="sm" data-testid="filter-xlsx-target-add"
              onClick={() => onChange({ targetColumns: [...rows, { language: '', column: '' }] })}>
              Add language
            </Button>
          </div>
        )}
      </div>
    </FormField>
  )
}

/** A list of names typed in one by one: elements, attributes or markers. */
function NameList({ label, hint, value, onChange, readOnly, error, placeholder, testId, field }: {
  label: string; hint: string; value: string[]; onChange: (v: string[]) => void; readOnly: boolean
  error?: string; placeholder: string; testId: string; field: string
}) {
  const options: ComboboxOption[] = value.map(v => ({ value: v, label: v }))
  return (
    <FormField label={label} error={error}>
      <div data-field={field}>
        <MultiCombobox value={value} options={options} wrap disabled={readOnly}
          placeholder={placeholder} searchPlaceholder="Type a name" createOptionLabel={'Add "{}"'}
          onCreateOption={text => { const v = text.trim(); if (v && !value.includes(v)) onChange([...value, v]) }}
          onChange={onChange} aria-label={label} aria-invalid={!!error} data-testid={testId} />
        <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
      </div>
    </FormField>
  )
}

function HTMLFields({ value, onChange, readOnly, errors }: FieldProps) {
  const rows = value.htmlAttributes
  const setRow = (i: number, row: Partial<(typeof rows)[number]>) =>
    onChange({ htmlAttributes: rows.map((r, j) => (j === i ? { ...r, ...row } : r)) })
  return (
    <div className="grid gap-4" data-testid="filter-html-options">
      <FormField label="Attributes to translate" error={errors?.htmlAttributes}>
        <div data-field="htmlAttributes" className="grid gap-2">
          <p className="text-xs text-muted-foreground">
            Text between tags is always translated. These attributes are translated too, per element. Only text
            attributes can be added, such as alt, title, placeholder, label and aria-label, and value on an input; the
            attributes of meta, link, base, html and head cannot.
          </p>
          <TooltipProvider delay={200}>
            {rows.map((row, i) => (
              <div key={i} className="grid grid-cols-[10rem_1fr_auto] items-start gap-2" data-testid={`filter-html-row-${i}`}>
                <Input value={row.element} disabled={readOnly} placeholder="Element, such as img"
                  aria-label="Element" onChange={e => setRow(i, { element: e.target.value })} />
                <MultiCombobox value={row.attributes} options={row.attributes.map(a => ({ value: a, label: a }))} wrap
                  disabled={readOnly} placeholder="Attributes, such as alt" searchPlaceholder="Type an attribute"
                  createOptionLabel={'Add "{}"'} aria-label={`Attributes of ${row.element || 'the element'}`}
                  onCreateOption={text => { const a = text.trim().toLowerCase(); if (a && !row.attributes.includes(a)) setRow(i, { attributes: [...row.attributes, a] }) }}
                  onChange={attributes => setRow(i, { attributes })} />
                {!readOnly && (
                  <RowDeleteAction label={`Delete ${row.element || 'this element'}`} testId={`filter-html-row-delete-${i}`}
                    onClick={() => onChange({ htmlAttributes: rows.filter((_, j) => j !== i) })} />
                )}
              </div>
            ))}
          </TooltipProvider>
          {!readOnly && (
            <div>
              <Button variant="outline" size="sm" data-testid="filter-html-add"
                onClick={() => onChange({ htmlAttributes: [...rows, { element: '', attributes: [] }] })}>
                Add element
              </Button>
            </div>
          )}
        </div>
      </FormField>
      <CheckboxField checked={value.metaDescription} onChange={metaDescription => onChange({ metaDescription })}
        disabled={readOnly} data-testid="filter-html-meta" label="Translate the meta description"
        description='The page summary in <meta name="description">, which search engines show.' />
      <CheckboxField checked={value.skipPre} onChange={skipPre => onChange({ skipPre })}
        disabled={readOnly} data-testid="filter-html-skip-pre" label="Leave preformatted text as is"
        description="Text in <pre>, such as code samples, is not translated." />
    </div>
  )
}

function XMLFields({ value, onChange, readOnly, errors }: FieldProps) {
  return (
    <div className="grid gap-4" data-testid="filter-xml-options">
      <p className="text-xs text-muted-foreground">
        Every element with text is translated, except what these lists leave out. Names are case-sensitive, as in XML.
      </p>
      <NameList label="Elements to skip" field="xmlSkip" testId="filter-xml-skip" readOnly={readOnly}
        value={value.xmlSkip} onChange={xmlSkip => onChange({ xmlSkip })} error={errors?.xmlSkip} placeholder="None"
        hint="Not translated, with everything inside them; kept as they are in the translated file." />
      <NameList label="Do-not-translate markers" field="xmlNoTranslate" testId="filter-xml-no-translate" readOnly={readOnly}
        value={value.xmlNoTranslate} onChange={xmlNoTranslate => onChange({ xmlNoTranslate })} error={errors?.xmlNoTranslate}
        placeholder="None" hint="attribute=value, such as translate=no: an element that has it is not translated." />
      <NameList label="Inline elements" field="xmlInline" testId="filter-xml-inline" readOnly={readOnly}
        value={value.xmlInline} onChange={xmlInline => onChange({ xmlInline })} error={errors?.xmlInline} placeholder="None"
        hint="Formatting inside a sentence, such as b or em: a tag in the segment instead of a new segment." />
      <NameList label="Reference elements" field="xmlRefElements" testId="filter-xml-ref-elements" readOnly={readOnly}
        value={value.xmlRefElements} onChange={xmlRefElements => onChange({ xmlRefElements })} error={errors?.xmlRefElements}
        placeholder="None" hint="Cross-references whose text comes from elsewhere: one tag, not translated." />
      <NameList label="Reference attributes" field="xmlRefAttributes" testId="filter-xml-ref-attributes" readOnly={readOnly}
        value={value.xmlRefAttributes} onChange={xmlRefAttributes => onChange({ xmlRefAttributes })} error={errors?.xmlRefAttributes}
        placeholder="None" hint="An element with one of these attributes set is a cross-reference too." />
    </div>
  )
}
