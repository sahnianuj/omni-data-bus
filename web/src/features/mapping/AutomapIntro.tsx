import { Sparkles, X } from 'lucide-react'
import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import type { MappingStats } from './types'

const key = (id: string) => `oc.seenAutomapIntro.${id}`

function seen(id: string): boolean {
  try {
    return window.localStorage.getItem(key(id)) === '1'
  } catch {
    return false // storage unavailable: show it (dismissal just won't persist)
  }
}

function remember(id: string) {
  try {
    window.localStorage.setItem(key(id), '1')
  } catch {
    /* ignore */
  }
}

/** First-run callout after creation (OC-018 AC5), shown once per mapping. */
export function AutomapIntro({ mappingId, stats, onReview }: { mappingId: string; stats: MappingStats; onReview: () => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(() => !seen(mappingId) && stats.autoMapped + stats.suggested > 0)
  if (!open) return null
  const close = () => {
    remember(mappingId)
    setOpen(false)
  }
  return (
    <div role="status" className="mx-6 mt-4 flex items-center gap-3 rounded-lg border border-primary/30 bg-primary-soft px-4 py-3 text-[13px] text-primary-soft-foreground">
      <Sparkles className="size-4 shrink-0" aria-hidden />
      <p className="flex-1">
        <Trans
          i18nKey="mapping.intro"
          values={{ auto: stats.autoMapped, total: stats.totalSourceColumns, count: stats.suggested }}
          components={{ b: <strong /> }}
        />
      </p>
      {stats.suggested > 0 ? (
        <Button
          size="sm"
          onClick={() => {
            close()
            onReview()
          }}
        >
          {t('mapping.reviewSuggestions')}
        </Button>
      ) : null}
      <Button variant="ghost" size="icon" aria-label={t('mapping.dismiss')} onClick={close}>
        <X aria-hidden />
      </Button>
    </div>
  )
}
