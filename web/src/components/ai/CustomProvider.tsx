import { useRef, useState, type ChangeEvent } from 'react'
import { Plus, Upload } from 'lucide-react'
import {
  uploadExtension, deleteExtension,
  type Extension, type ExtensionCapability, type AIVendorStatus,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { serverFieldError } from '@/lib/server-field-error'
import ConfirmDialog from '../ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Chip } from '@/components/ui/chip'
import { vendorLabel } from './vendor-catalog'

// A custom provider is an installed extension: an uploaded zip that adds a
// QE/APE provider to the grid. Uploading a zip whose id is already installed
// replaces it and keeps its keys and engines.

const CAPABILITY_LABELS: Record<ExtensionCapability, string> = { qe: 'QE', ape: 'APE' }

// The allowed_hosts entry that stands for the host of the key's endpoint.
const BASE_URL_HOST = '{settings.base_url}'

/** The hosts a custom provider may call, in words: a template entry names the endpoint set on its key. */
function sendsDataTo(hosts: string[]): string {
  return hosts.map(h => (h === BASE_URL_HOST ? 'the endpoint set on its key' : h)).join(', ')
}

/**
 * Uploads a custom provider zip through a hidden file input. `installed` holds
 * the providers already in the grid, so the toast says whether the zip added a
 * provider or updated one. A refused zip is returned as `error`, for the caller
 * to show next to the control that picked it.
 */
function useCustomProviderUpload({ installed, testId, onUploaded, onRefused }: {
  installed: ReadonlySet<string>
  testId: string
  onUploaded?: (ext: Extension) => void
  onRefused?: () => void
}) {
  const fileInput = useRef<HTMLInputElement>(null)
  const mut = useMutationWithToast({
    mutationFn: async (file: File) => {
      const ext = await uploadExtension(file)
      return { ext, replaced: installed.has(ext.provider) }
    },
    successMessage: ({ ext, replaced }) =>
      replaced ? `${ext.name} updated to ${ext.version}` : `${ext.name} ${ext.version} added`,
    errorToast: false,
    invalidateKeys: [queryKeys.aiVendors.all],
    onSuccess: ({ ext }) => onUploaded?.(ext),
    onError: () => onRefused?.(),
  })

  const onFile = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // Cleared so picking the same file again (after fixing it) fires a change.
    e.target.value = ''
    if (file) mut.mutate(file)
  }

  const input = (
    <input
      ref={fileInput}
      type="file"
      accept=".zip,application/zip"
      className="hidden"
      onChange={onFile}
      data-testid={testId}
    />
  )
  return {
    input,
    pick: () => fileInput.current?.click(),
    isPending: mut.isPending,
    error: serverFieldError(mut.error),
  }
}

function UploadError({ message, testId }: { message: string; testId: string }) {
  return (
    <p role="alert" className="text-xs text-red-500 dark:text-red-400" data-testid={testId}>
      {message}
    </p>
  )
}

/**
 * The grid cell that adds a custom provider. Owner/admin only. The button
 * opens a short explanation first; its "Choose zip file" opens the picker. A
 * refused zip is shown in that dialog.
 */
export function AddCustomProvider({ installed, onAdded }: {
  installed: ReadonlySet<string>
  onAdded: (ext: Extension) => void
}) {
  const [open, setOpen] = useState(false)
  const upload = useCustomProviderUpload({
    installed,
    testId: 'ai-custom-provider-file',
    onUploaded: ext => {
      setOpen(false)
      onAdded(ext)
    },
    onRefused: () => setOpen(true),
  })
  return (
    <div className="flex flex-col gap-1">
      {upload.input}
      <Button
        variant="outline"
        className="w-full flex-1"
        loading={upload.isPending}
        onClick={() => setOpen(true)}
        data-testid="ai-custom-provider-add"
      >
        {!upload.isPending && <Plus />} {upload.isPending ? 'Uploading…' : 'Add custom provider'}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md" data-testid="ai-custom-provider-dialog">
          <DialogHeader>
            <DialogTitle>Add a custom provider</DialogTitle>
            <DialogDescription>
              A custom provider adds quality estimation (QE) or automatic post-editing (APE) from another vendor.
            </DialogDescription>
          </DialogHeader>
          {upload.error && <UploadError message={upload.error.message} testId="ai-custom-provider-add-error" />}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button loading={upload.isPending} onClick={upload.pick} data-testid="ai-custom-provider-choose">
              {!upload.isPending && <Upload />} {upload.isPending ? 'Uploading…' : 'Choose zip file'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/**
 * The top of a custom provider's panel: version, what it can do and where it
 * sends data. `canWrite` (owner/admin) adds Replace and Remove.
 */
export function CustomProviderDetails({ vendor, installed, canWrite, onRemoved }: {
  vendor: AIVendorStatus & { extension: NonNullable<AIVendorStatus['extension']> }
  installed: ReadonlySet<string>
  canWrite: boolean
  onRemoved: () => void
}) {
  const ext = vendor.extension
  const name = vendorLabel(vendor)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const replace = useCustomProviderUpload({ installed, testId: 'ai-custom-provider-replace-file' })
  const removeMut = useMutationWithToast({
    mutationFn: () => deleteExtension(ext.id),
    successMessage: `${name} removed`, errorMessage: 'Remove failed',
    // Its keys go with it, so engines and the routers that list them change.
    invalidateKeys: [queryKeys.aiVendors.all, queryKeys.aiEngines.all, queryKeys.aiRouters.all, queryKeys.mtRouters.all],
    onSuccess: () => { setConfirmRemove(false); onRemoved() },
  })

  return (
    <div className="space-y-1" data-testid="ai-custom-provider-details">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1 text-xs text-muted-foreground">
          <div className="flex flex-wrap items-center gap-2">
            <span>Version {ext.version}</span>
            {ext.capabilities.map(c => <Chip key={c}>{CAPABILITY_LABELS[c] ?? c}</Chip>)}
          </div>
          {ext.allowed_hosts.length > 0 && (
            <p className="truncate" title={sendsDataTo(ext.allowed_hosts)}>
              Sends data to: {sendsDataTo(ext.allowed_hosts)}
            </p>
          )}
        </div>
        {canWrite && (
          <div className="flex shrink-0 items-center gap-1.5">
            {replace.input}
            <Button
              variant="outline"
              size="xs"
              loading={replace.isPending}
              onClick={replace.pick}
              data-testid="ai-custom-provider-replace"
            >
              {!replace.isPending && <Upload />} Replace
            </Button>
            <Button
              variant="destructive"
              size="xs"
              onClick={() => setConfirmRemove(true)}
              data-testid="ai-custom-provider-remove"
            >
              Remove
            </Button>
          </div>
        )}
      </div>
      {replace.error && <UploadError message={replace.error.message} testId="ai-custom-provider-replace-error" />}
      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title={`Remove ${name}?`}
        description="Its API keys are deleted. Engines that use them stop until you choose a new key for them."
        actionLabel="Remove"
        onConfirm={() => removeMut.mutate()}
      />
    </div>
  )
}
