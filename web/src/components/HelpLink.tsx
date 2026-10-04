import { CircleHelp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { helpUrl, type HelpTopic } from '@/lib/helpLinks'
import { Tooltip } from './ui/tooltip'

/** The "?" next to a feature: opens its user-guide chapter in a new tab (OC-057). */
export function HelpLink({ topic }: { topic: HelpTopic }) {
  const { t } = useTranslation()
  const label = t(`help.${topic}`)
  return (
    <Tooltip content={label}>
      <a
        href={helpUrl(topic)}
        target="_blank"
        rel="noreferrer"
        aria-label={label}
        className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-subtle hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
      >
        <CircleHelp aria-hidden className="size-4" />
      </a>
    </Tooltip>
  )
}
