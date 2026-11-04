import { useState, type ReactNode } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Button, MISSING_REQUIRED_REASON } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { createInvite } from '../../api'
import { roleLabel } from '../../lib/roles'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'

// The server's limit on a vendor org's name.
const ORG_NAME_MAX = 120

function inviteSchema(withOrgName: boolean) {
  return z.object({
    orgName: withOrgName
      ? z.string().trim().min(1, 'Name is required').max(ORG_NAME_MAX, `At most ${ORG_NAME_MAX} characters`)
      : z.string(),
    email: z.string().email('Valid email is required'),
    role: z.string().min(1),
  })
}
type InviteFormValues = z.infer<ReturnType<typeof inviteSchema>>

/**
 * A button in the Members list's header that opens an email invite form in a
 * popup. With one role there is no role picker: every invite gets that role.
 * `orgNameField` adds a required name field before the email: the invite then
 * creates an org of that name (a vendor org), with the invitee as its admin.
 * The popup closes once the invite is sent. No useScrollToError: it focuses
 * the first invalid field whenever the errors change, so leaving the empty
 * first field for the second one would pull the cursor back; the errors show
 * under each field of this short form instead.
 */
export function InviteDialog({
  title, description, icon, roles, placeholder, testIdPrefix, guide, onInvited, emailLabel = 'Email', orgNameField,
}: {
  title: string
  /** What the invite does, under the popup's title. */
  description: ReactNode
  icon: ReactNode
  roles: [string, ...string[]]
  placeholder: string
  testIdPrefix: string
  guide: string
  onInvited: () => void
  emailLabel?: string
  orgNameField?: { label: string; placeholder: string }
}) {
  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isValid },
  } = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema(!!orgNameField)),
    // Validate on submit, not on blur: closing the popup takes the focus out of
    // a field, and a blur check would flash its error as the popup fades. Send
    // stays disabled, with the reason, until the form is valid.
    mode: 'onSubmit',
    defaultValues: { orgName: '', email: '', role: roles[0] },
  })
  const [open, setOpen] = useState(false)

  const inviteMut = useMutationWithToast({
    mutationFn: (v: InviteFormValues) => createInvite(v.email, v.role, orgNameField ? v.orgName : undefined),
    successMessage: (_, v) => `Invite sent to ${v.email}`,
    errorMessage: 'Failed to create invite',
    invalidateKeys: [queryKeys.invites.all],
    onSuccess: () => {
      reset()
      setOpen(false)
      onInvited()
    },
  })
  const close = (next: boolean) => {
    setOpen(next)
    if (!next) reset()
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)} data-testid={`${testIdPrefix}-open`} data-guide={guide} startIcon={icon}>
        {title}
      </Button>
      <Dialog open={open} onOpenChange={close} keepOpen={inviteMut.isPending}>
        <DialogContent className="sm:max-w-md" data-testid={`${testIdPrefix}-dialog`}>
          <form onSubmit={handleSubmit(v => inviteMut.mutate({ ...v, email: v.email.trim() }))}>
            <DialogHeader>
              <DialogTitle>{title}</DialogTitle>
              <DialogDescription>{description}</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              {orgNameField && (
                <FormField label={orgNameField.label} error={errors.orgName?.message} required>
                  <Input
                    {...register('orgName')}
                    placeholder={orgNameField.placeholder}
                    maxLength={ORG_NAME_MAX}
                    data-testid={`${testIdPrefix}-org-name`}
                  />
                </FormField>
              )}
              <FormField label={emailLabel} error={errors.email?.message} required>
                <Input {...register('email')} type="email" placeholder={placeholder} data-testid={`${testIdPrefix}-email`} />
              </FormField>
              {roles.length > 1 && (
                <FormField label="Role" error={errors.role?.message}>
                  <Controller
                    control={control}
                    name="role"
                    render={({ field }) => (
                      <Combobox
                        value={field.value}
                        onChange={field.onChange}
                        options={roles.map(value => ({ value, label: roleLabel(value) }))}
                        searchable={false}
                        className="w-full"
                        data-testid={`${testIdPrefix}-role`}
                      />
                    )}
                  />
                </FormField>
              )}
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => close(false)} disabled={inviteMut.isPending}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={inviteMut.isPending || !isValid}
                disabledReason={isValid ? undefined : orgNameField ? MISSING_REQUIRED_REASON : 'Enter a valid email address first.'}
                loading={inviteMut.isPending}
                data-testid={`${testIdPrefix}-submit`}
              >
                Send invite
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
