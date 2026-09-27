import { useState, type ReactNode } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Button } from './ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog'
import { Input } from './ui/input'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  confirmLabel: ReactNode
  onConfirm: () => void
  destructive?: boolean
  loading?: boolean
  /** When set, the user must type this exact text to enable the confirm button. */
  requireTypedName?: string
}

export function ConfirmDialog(props: Props) {
  const { open, onOpenChange, title, description, confirmLabel, onConfirm, destructive, loading, requireTypedName } = props
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')
  const blocked = requireTypedName !== undefined && typed !== requireTypedName
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setTyped('')
        onOpenChange(o)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {requireTypedName !== undefined ? (
          <label className="flex flex-col gap-2 text-sm text-muted">
            <span>
              <Trans i18nKey="common.typeToConfirm" values={{ name: requireTypedName }} components={{ 1: <strong className="font-mono text-foreground" /> }} />
            </span>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
          </label>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant={destructive ? 'destructive' : 'primary'} disabled={blocked} loading={loading} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
