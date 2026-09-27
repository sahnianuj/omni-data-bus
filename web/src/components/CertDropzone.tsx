import { FileKey2, X } from 'lucide-react'
import { useId, useRef, useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

const PEM_PREFIX = '-----BEGIN CERTIFICATE-----'

/** isPem reports whether text looks like a PEM certificate (the server validates it fully). */
export function isPem(text: string) {
  return text.trimStart().startsWith(PEM_PREFIX)
}

type Props = {
  label: string
  /** Fingerprint of the certificate already stored, if any. */
  storedFingerprint?: string
  onChange: (pem: string | null) => void
}

/** Drag-and-drop (or browse) for CA / client certificates, read client-side (OC-006 AC5). */
export function CertDropzone({ label, storedFingerprint, onChange }: Props) {
  const { t } = useTranslation()
  const inputId = useId()
  const input = useRef<HTMLInputElement>(null)
  const [error, setError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)

  async function accept(file: File | undefined) {
    if (!file) return
    const text = await file.text()
    if (!isPem(text)) {
      setError(t('cert.invalid'))
      setLoaded(null)
      onChange(null)
      return
    }
    setError(null)
    setLoaded(file.name)
    onChange(text)
  }

  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    void accept(e.dataTransfer.files[0])
  }

  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[13px] font-medium">{label}</span>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        data-testid="cert-dropzone"
        className={cn(
          'flex items-center gap-3 rounded-[var(--radius-control)] border border-dashed border-border-strong px-3 py-3 text-[13px] text-muted',
          dragging && 'border-ring bg-primary-soft',
          error && 'border-danger-solid',
        )}
      >
        <FileKey2 className="size-4 shrink-0 text-subtle" aria-hidden />
        {loaded ? (
          <span className="flex-1 text-foreground">
            {t('cert.loaded')}: <span className="font-mono">{loaded}</span>
          </span>
        ) : storedFingerprint ? (
          <span className="flex-1">
            {t('cert.stored')}: <span className="font-mono text-foreground">{storedFingerprint.slice(0, 29)}…</span>
          </span>
        ) : (
          <span className="flex-1">
            {t('cert.drop')}{' '}
            <label htmlFor={inputId} className="cursor-pointer font-medium text-primary-soft-foreground underline-offset-4 hover:underline">
              {t('cert.browse')}
            </label>
          </span>
        )}
        {loaded ? (
          <button
            type="button"
            aria-label={t('cert.remove')}
            onClick={() => {
              setLoaded(null)
              onChange(null)
              if (input.current) input.current.value = ''
            }}
            className="rounded p-1 text-subtle hover:text-foreground"
          >
            <X className="size-3.5" aria-hidden />
          </button>
        ) : null}
        <input
          ref={input}
          id={inputId}
          type="file"
          accept=".pem,.crt,.cer,application/x-pem-file"
          className="sr-only"
          aria-label={label}
          onChange={(e) => void accept(e.target.files?.[0])}
        />
      </div>
      {error ? (
        <p role="alert" className="text-xs text-danger">
          {error}
        </p>
      ) : null}
    </div>
  )
}
