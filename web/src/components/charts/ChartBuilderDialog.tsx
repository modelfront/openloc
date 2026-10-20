// "New chart".
//
// The whole vocabulary comes from /api/dashboards/metrics-catalog, which the
// server generates from the metrics registry: a measure added in Go shows up
// here with no frontend change, and cannot be offered with a dimension it does
// not have.
//
// The form asks for the metric, how to draw it, what the axis is, which units
// to show, and which filters the card carries. Every field narrows the
// next one — choosing a measure resets the rest, because a dimension that made
// sense for the previous one usually does not exist on the new one, and the
// chart type decides what an axis can even be.
//
// The rule behind the narrowing: only offer combinations that produce a chart
// someone can read. A dropdown that changes nothing, or an option that draws a
// misleading picture, costs more than the flexibility it buys — the chat's
// create_chart is still there for the rare shape this form will not build.
//
// There is no date-range field. The dashboard's date-range control owns the
// window for every card on it, so a chart carrying its own would be overridden
// the moment it was rendered.
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AreaChart, BarChart3, LineChart, PieChart, Loader2Icon } from 'lucide-react'
import { createChart, updateChart, getMetricsCatalog, suggestChart, type CatalogMeasure, type CatalogValue, type DashboardChart } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { useScrollToError } from '@/hooks/useScrollToError'
import { metricLabel } from '@/lib/metric-labels'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox } from '@/components/ui/combobox'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { CONTROL_LABELS } from './DashboardFilterBar'
import { cn } from '@/lib/utils'
import { openChatWithPrompt, openChatWithHint } from '@/lib/chat-bus'
import { useGuideReveal } from '@/lib/guide-reveal'
import { TextLink } from '@/components/ui/text-link'

const CHART_TYPES = [
  { value: 'line', label: 'Line', icon: LineChart },
  { value: 'bar', label: 'Bar', icon: BarChart3 },
  { value: 'area', label: 'Area', icon: AreaChart },
  { value: 'pie', label: 'Pie', icon: PieChart },
] as const

const GRAIN_LABELS: Record<string, string> = { day: 'Date (daily)', week: 'Date (weekly)', month: 'Date (monthly)' }

const NO_SPLIT = ''

const schema = z.object({
  measure: z.string().min(1, 'Pick a metric'),
  chartType: z.enum(['line', 'bar', 'area', 'pie']),
  xKey: z.string().min(1, 'Pick what goes on the axis'),
  seriesKey: z.string(),
  series: z.array(z.string()).min(1, 'Pick at least one unit'),
  controls: z.array(z.string()),
  title: z.string().trim().min(1, 'Title is required'),
})
type Values = z.infer<typeof schema>

const EMPTY: Values = { measure: '', chartType: 'line', xKey: 'week', seriesKey: NO_SPLIT, series: [], controls: [], title: '' }

const fromChart = (c: DashboardChart): Values => ({
  measure: c.definition.measure,
  chartType: c.viz.type,
  xKey: c.viz.x_key,
  seriesKey: c.viz.series_key ?? NO_SPLIT,
  series: c.viz.series,
  controls: c.controls.controls,
  title: c.title,
})

type Option = { value: string; label: string }

const asOptions = (keys: string[]): Option[] => keys.map(k => ({ value: k, label: metricLabel(k) }))
const grainOptions = (grains: string[]): Option[] =>
  grains.map(g => ({ value: g, label: GRAIN_LABELS[g] ?? metricLabel(g) }))

/**
 * What can go on the axis, which depends on how the chart is drawn:
 *
 *   line / area  a date. Both imply continuity, so a category axis draws a
 *                trend across things that have no order.
 *   bar          either. Bars are discrete, so "words per engine" reads fine.
 *   pie          a category. Slices are parts of one whole; time is not.
 */
export function axisOptions(measure: CatalogMeasure | undefined, grains: string[], chartType: string): Option[] {
  const dims = asOptions(measure?.dimensions ?? [])
  if (chartType === 'pie') return dims
  if (chartType === 'bar') return [...grainOptions(grains), ...dims]
  return grainOptions(grains)
}

/**
 * The axis to start on: weekly when the type allows a date, otherwise the
 * first category. Weekly because the default window is 90 days — daily over it
 * is 90 points, monthly is three.
 */
export function defaultAxis(options: Option[]): string {
  return (options.find(o => o.value === 'week') ?? options[0])?.value ?? ''
}

/**
 * Which values may be plotted together: only those counting the SAME thing.
 * Input and output tokens share an axis; words and segments do not — added up
 * they are a number of nothing. Picking across units replaces the selection
 * rather than refusing it, so the form never has to say no.
 */
export function toggleValue(values: CatalogValue[], selected: string[], name: string, splitting: boolean): string[] {
  // A split already draws one series per category value, reading a single
  // column, so there is nothing to combine with.
  if (splitting) return [name]
  if (selected.includes(name)) {
    const next = selected.filter(s => s !== name)
    return next.length ? next : selected
  }
  const unit = values.find(v => v.name === name)?.unit
  const sameUnit = selected.every(s => values.find(v => v.name === s)?.unit === unit)
  return sameUnit ? [...selected, name] : [name]
}

/**
 * What the assistant link prefills when no tailored suggestion is available: a
 * request shaped the way the assistant builds charts — measure, breakdown,
 * period, and the dashboard to save it to. Prefilled, not sent, so it is a
 * starting point to edit.
 */
export function assistantExample(dashboardName?: string): string {
  const where = dashboardName ? `the "${dashboardName}" dashboard` : 'this dashboard'
  return `Chart post-editing volume per week, broken down by target language, for the last 90 days, and add it to ${where}.`
}

/** The metric a guided tour picks to show the chart's options. */
const TOUR_MEASURE = 'words_processed'

export function ChartBuilderDialog({ open, onClose, dashboardId, dashboardName, chart, guided = false }: {
  open: boolean
  onClose: () => void
  dashboardId: number
  /** Named in the assistant's fallback prompt, so its chart lands here. */
  dashboardName?: string
  /** Editing an existing chart rather than creating one. The same form either
   *  way: a chart IS its selections, so the thing that made it is the thing
   *  that corrects it. */
  chart?: DashboardChart
  /** Opened by a guided tour: not modal, so the tour's card stays clickable,
   *  and a click on that card does not close it. */
  guided?: boolean
}) {
  const queryClient = useQueryClient()
  const { data: catalog, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.dashboards.catalog,
    queryFn: getMetricsCatalog,
    // The registry only changes on deploy.
    staleTime: Infinity,
    enabled: open,
  })

  const { register, handleSubmit, reset, setValue, getValues, watch, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: EMPTY,
  })
  useScrollToError(errors)
  const { measure: measureName, chartType, xKey, seriesKey, series, controls } = watch()
  // setValue marks the field for re-validation only once a submit has failed,
  // so a fixed error clears the moment it is fixed.
  const set = <K extends keyof Values>(key: K, value: Values[K]) => setValue(key, value as never, { shouldValidate: !!errors[key] })

  const measure = catalog?.measures.find(m => m.name === measureName)
  // By the name a person reads, not the key or the server's order.
  const sortedMeasures = useMemo(
    () => [...(catalog?.measures ?? [])].sort((a, b) => metricLabel(a.name).localeCompare(metricLabel(b.name))),
    [catalog],
  )
  const grains = catalog?.grains ?? []
  const isPie = chartType === 'pie'
  const axes = axisOptions(measure, grains, chartType)

  // Selecting a measure seeds the rest of the form from what that measure can
  // actually do — a first useful chart that only needs a press of Create.
  const pickMeasure = (m: CatalogMeasure) => {
    const title = getValues('title')
    set('measure', m.name)
    set('series', m.values.slice(0, 1).map(v => v.name))
    set('seriesKey', NO_SPLIT)
    set('controls', [])
    set('xKey', defaultAxis(axisOptions(m, grains, chartType)))
    // The title follows the metric while it is still the suggestion (empty, or
    // the current metric's name); a title someone wrote stays.
    if (!title.trim() || title === metricLabel(measureName)) set('title', metricLabel(m.name))
  }

  // The axis and the split are drawn from what the TYPE allows, so switching to
  // a pie has to drop a weekly axis rather than keep an unofferable value.
  const pickChartType = (next: Values['chartType']) => {
    set('chartType', next)
    const allowed = axisOptions(measure, grains, next)
    if (!allowed.some(o => o.value === xKey)) set('xKey', defaultAxis(allowed))
    if (next === 'pie') set('seriesKey', NO_SPLIT)
  }

  // Opening seeds the form: from the chart when editing, empty when creating.
  // Keyed on `open` so reopening after a cancel starts clean rather than
  // resuming a half-made chart nobody remembers abandoning.
  useEffect(() => {
    if (open) reset(chart ? fromChart(chart) : EMPTY)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on open
  }, [open])

  // A tour step on the chart's options needs a metric to show them: it picks
  // one that offers every kind of option.
  const [showOptions, setShowOptions] = useState(false)
  useGuideReveal('chart_builder.options', () => setShowOptions(true), () => setShowOptions(false))
  useEffect(() => {
    if (!showOptions || !open || !catalog || measureName) return
    const sample = catalog.measures.find(m => m.name === TOUR_MEASURE) ?? catalog.measures[0]
    if (sample) pickMeasure(sample)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- pickMeasure reads the form
  }, [showOptions, open, catalog, measureName])

  const saveMut = useMutationWithToast({
    mutationFn: async (v: Values) => {
      const spec = {
        title: v.title.trim(),
        measure: v.measure,
        // A time grain is a dimension to the compiler, so the axis and the
        // split are simply the dimension list.
        dimensions: [v.xKey, v.seriesKey].filter(Boolean),
        chart_type: v.chartType,
        x_key: v.xKey,
        series: v.series,
        series_key: v.seriesKey || undefined,
        controls: v.controls,
      }
      if (chart) await updateChart(chart.id, spec)
      else await createChart(dashboardId, spec)
    },
    successMessage: chart ? 'Chart saved' : 'Chart added',
    errorMessage: 'Failed to save the chart',
    // The whole dashboard, not just this card: an edited chart changes what
    // controls the dashboard-level bar can offer.
    invalidateKeys: [queryKeys.dashboards.all],
    onSuccess: onClose,
  })
  const submit = handleSubmit(v => { if (!saveMut.isPending) saveMut.mutate(v) })

  // The assistant writes a prompt for a chart this dashboard does not have yet.
  // One model call per click, cached like the prompt
  // cards; if it cannot answer, the fixed example still gets the user going.
  const [asking, setAsking] = useState(false)
  const askAssistant = async () => {
    setAsking(true)
    try {
      const s = await queryClient.fetchQuery({
        queryKey: queryKeys.dashboards.suggestion(dashboardId),
        queryFn: () => suggestChart(dashboardId),
        staleTime: 5 * 60_000,
      })
      onClose()
      if (s.prompt) openChatWithPrompt(s.prompt)
      else if (s.hint) openChatWithHint(s.hint)
      else openChatWithPrompt(assistantExample(dashboardName))
    } catch {
      onClose()
      openChatWithPrompt(assistantExample(dashboardName))
    } finally {
      setAsking(false)
    }
  }

  const splitOptions: Option[] = [
    { value: NO_SPLIT, label: 'No breakdown' },
    // Categories only, and never the axis itself. The catalog sends no grains
    // here: a chart split by week draws a band per week.
    ...asOptions((measure?.dimensions ?? []).filter(d => d !== xKey)),
  ]
  // Granularity swaps one time bucket for another, so it means nothing unless
  // the axis IS time. The server applies the same rule to what it stores.
  const offeredFilters = (measure?.filters ?? []).filter(
    c => c !== 'granularity' || grains.includes(xKey),
  )

  return (
    <Dialog open={open} onOpenChange={v => { if (!v) onClose() }} modal={!guided} disablePointerDismissal={guided} keepOpen={saveMut.isPending}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{chart ? 'Edit chart' : 'New chart'}</DialogTitle>
        </DialogHeader>
        {isError && !catalog ? (
          <LoadFailed what="metrics" error={error} onRetry={() => refetch()} />
        ) : isLoading || !catalog ? (
          <LoadingPlaceholder />
        ) : (
          <div className="grid gap-5 sm:grid-cols-[13rem_1fr]">
            {/* The metric first: everything else is a property of it. */}
            <FormField label="Metric" error={errors.measure?.message} required>
              <div
                role="radiogroup"
                aria-label="Metric"
                className="max-h-96 overflow-y-auto rounded-md border border-gray-200 dark:border-gray-800"
                data-testid="measure-list"
                data-guide="chart_builder.metrics"
                data-field="measure"
              >
                {sortedMeasures.map(m => (
                  // eslint-disable-next-line no-restricted-syntax -- a radio in a list, not an action button
                  <button
                    key={m.name}
                    type="button"
                    role="radio"
                    aria-checked={m.name === measureName}
                    onClick={() => pickMeasure(m)}
                    data-testid={`measure-${m.name}`}
                    className={cn(
                      'block w-full px-3 py-2 text-left text-sm transition-colors',
                      m.name === measureName
                        ? 'bg-accent font-medium text-foreground'
                        : 'text-gray-600 hover:bg-accent/50 dark:text-gray-300',
                    )}
                  >
                    {metricLabel(m.name)}
                  </button>
                ))}
              </div>
            </FormField>

            {!measure ? (
              <div className="self-center text-sm text-gray-400" data-testid="builder-empty">
                <p>Pick a metric on the left, or skip the form and ask the assistant.</p>
                {/* Creating only: an edited chart always has its metric. */}
                {!chart && (
                  <TextLink
                    className="mt-1 inline-flex items-center gap-1"
                    disabled={asking}
                    data-testid="builder-ask-assistant"
                    onClick={askAssistant}
                  >
                    {asking && <Loader2Icon className="size-3.5 animate-spin" aria-hidden />}
                    Ask the assistant
                  </TextLink>
                )}
              </div>
            ) : (
              <div className="min-w-0 space-y-4" data-guide="chart_builder.options">
                {/* The registry's own words, so the chart is built on what the
                    number means rather than on a guess from its name. */}
                <p className="text-xs leading-relaxed text-gray-500 dark:text-gray-400" data-testid="measure-definition">
                  {measure.definition}
                </p>

                <FormField label="Title" error={errors.title?.message} required>
                  <Input
                    {...register('title')}
                    data-testid="chart-title"
                    onKeyDown={e => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        submit()
                      }
                    }}
                  />
                </FormField>

                <FormField label="Chart type">
                  <ChoiceToggle
                    value={chartType}
                    onChange={pickChartType}
                    aria-label="Chart type"
                    data-testid="chart-type"
                    options={CHART_TYPES.map(t => ({ value: t.value, label: t.label, icon: t.icon }))}
                  />
                </FormField>

                <div className="grid gap-3 sm:grid-cols-2">
                  {/* A pie has no axis — its x_key names what each SLICE is. */}
                  <FormField label={isPie ? 'One slice per' : 'X axis'} error={errors.xKey?.message} required>
                    <div data-field="xKey">
                      <Combobox
                        options={axes}
                        value={xKey}
                        onChange={v => { set('xKey', v); if (v === seriesKey) set('seriesKey', NO_SPLIT) }}
                        className="w-full"
                        aria-label={isPie ? 'One slice per' : 'X axis'}
                        data-testid="chart-x-key"
                      />
                    </div>
                  </FormField>
                  <FormField label="Breakdown by">
                    <Combobox
                      options={splitOptions}
                      value={seriesKey}
                      onChange={v => { set('seriesKey', v); if (v) set('series', series.slice(0, 1)) }}
                      // One ring of slices: there is no second axis to split along.
                      disabled={isPie}
                      placeholder={isPie ? 'Not for pie charts' : undefined}
                      className="w-full"
                      aria-label="Breakdown by"
                      data-testid="chart-series-key"
                    />
                  </FormField>
                </div>

                <FormField label="Units" error={errors.series?.message} required>
                  <div className="flex flex-wrap gap-x-5" data-testid="chart-values" data-field="series">
                    {measure.values.map(v => (
                      <CheckboxField
                        key={v.name}
                        checked={series.includes(v.name)}
                        onChange={() => set('series', toggleValue(measure.values, series, v.name, !!seriesKey))}
                        label={metricLabel(v.name)}
                        data-testid={`chart-value-${v.name}`}
                      />
                    ))}
                  </div>
                </FormField>

                {offeredFilters.length > 0 && (
                  <FormField label="Filters on this card">
                    <div className="flex flex-wrap gap-x-5" data-testid="builder-filters">
                      {offeredFilters.map(c => (
                        <CheckboxField
                          key={c}
                          checked={controls.includes(c)}
                          onChange={on => set('controls', on ? [...controls, c] : controls.filter(x => x !== c))}
                          label={CONTROL_LABELS[c] ?? metricLabel(c)}
                          data-testid={`builder-filter-${c}`}
                        />
                      ))}
                    </div>
                  </FormField>
                )}
              </div>
            )}
          </div>
        )}
        <DialogFooter className="mt-4">
          <Button type="button" variant="outline" onClick={onClose} disabled={saveMut.isPending}>Cancel</Button>
          {catalog && (
            <Button
              type="button"
              onClick={submit}
              loading={saveMut.isPending}
              data-testid="create-chart-submit"
            >
              {saveMut.isPending ? 'Saving…' : chart ? 'Save chart' : 'Create chart'}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
