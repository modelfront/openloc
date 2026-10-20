// AnalysisExportMenu — downloads a saved analysis as CSV or XLSX, from the
// icon beside Delete on its row in ProjectAnalysesTab. Only a completed
// analysis has numbers to export.

import { FileSpreadsheet, FileText } from 'lucide-react'

import { exportAnalysis, type Analysis, type AnalysisExportFormat } from '../api/analyses'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { DownloadOptionsMenu, type DownloadOption } from './ui/download-options-menu'

const OPTIONS: DownloadOption<AnalysisExportFormat>[] = [
  { id: 'xlsx', label: 'Excel (XLSX)', icon: FileSpreadsheet },
  { id: 'csv', label: 'CSV', icon: FileText },
]

export function AnalysisExportMenu({ analysis }: { analysis: Analysis }) {
  const download = useMutationWithToast<void, AnalysisExportFormat>({
    mutationFn: format => exportAnalysis(analysis.id, format),
    successMessage: (_, format) => `${OPTIONS.find(o => o.id === format)!.label} downloaded`,
    errorMessage: 'Could not download the analysis',
  })

  return (
    <DownloadOptionsMenu
      options={OPTIONS.map(o => ({ ...o, testId: `analysis-export-${analysis.id}-${o.id}` }))}
      onSelect={format => download.mutate(format)}
      busy={download.isPending}
      disabled={analysis.status !== 'completed'}
      variant="ghost"
      color="muted"
      hoverColor="muted"
      iconOnly
      ariaLabel={`Download ${analysis.name}`}
      testId={`analysis-export-${analysis.id}`}
    />
  )
}
