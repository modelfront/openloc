// Highlights the series under the pointer in an area or line chart. recharts
// drops mouse handlers on <Area> and <Line> (bars and pie slices keep theirs),
// so this reads the pointer from the chart instead: the tooltip's position,
// turned into a value on the Y axis, against the values at the hovered x.
// Renders nothing.
import { useEffect, useRef } from 'react'
import { useActiveTooltipCoordinate, useActiveTooltipDataPoints, useYAxisInverseScale } from 'recharts'
import { seriesAt } from '@/lib/chart-legend'

export function PointerSeries({ series, stacked, onHover }: {
  /** The series shown, in drawing order (bottom of a stack first). */
  series: string[]
  stacked: boolean
  onHover: (name: string | null) => void
}) {
  const coordinate = useActiveTooltipCoordinate()
  const toValue = useYAxisInverseScale()
  const points = useActiveTooltipDataPoints<Record<string, unknown>>()
  const row = points?.[0]
  const name = coordinate && toValue && row ? seriesAt(row, series, Number(toValue(coordinate.y)), stacked) : null

  // Report changes only, so a legend entry the pointer is on keeps its
  // highlight while the pointer is off the chart.
  const last = useRef<string | null>(null)
  useEffect(() => {
    if (name === last.current) return
    last.current = name
    onHover(name)
  }, [name, onHover])
  return null
}
