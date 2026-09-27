import { Eye, EyeOff } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from './ui/button'
import { Input } from './ui/input'

type Props = {
  id: string
  /** A secret is already stored for this field. */
  stored: boolean
  /** undefined = keep the stored value; a string = the new value. */
  value: string | undefined
  onChange: (value: string | undefined) => void
  invalid?: boolean
  'aria-describedby'?: string
}

/**
 * Write-only secret field (OC-006 AC5). A stored secret is never shown: the field displays a
 * placeholder and a Change button that swaps in an empty input.
 */
export function SecretInput({ id, stored, value, onChange, invalid, ...aria }: Props) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  const editing = !stored || value !== undefined

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        <Input id={id} value={t('secret.stored')} disabled readOnly aria-describedby={aria['aria-describedby']} />
        <Button variant="secondary" size="sm" onClick={() => onChange('')}>
          {t('secret.change')}
        </Button>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <div className="relative flex-1">
        <Input
          id={id}
          type={visible ? 'text' : 'password'}
          autoComplete="new-password"
          value={value ?? ''}
          invalid={invalid}
          onChange={(e) => onChange(e.target.value)}
          className="pr-9"
          aria-describedby={aria['aria-describedby']}
        />
        <button
          type="button"
          onClick={() => setVisible((v) => !v)}
          aria-label={visible ? t('secret.hide') : t('secret.show')}
          className="absolute right-1 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded text-subtle hover:text-foreground"
        >
          {visible ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
        </button>
      </div>
      {stored ? (
        <Button variant="ghost" size="sm" onClick={() => onChange(undefined)}>
          {t('secret.keepStored')}
        </Button>
      ) : null}
    </div>
  )
}
