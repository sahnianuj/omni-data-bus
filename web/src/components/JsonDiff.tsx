import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

type Line = { text: string; path: string; indent: number }

/** Pretty-prints a JSON value as lines, each tagged with the $-path of the value on it. */
function lines(value: unknown, path = '$', indent = 0, prefix = '', suffix = ''): Line[] {
  if (Array.isArray(value)) {
    if (value.length === 0) return [{ text: `${prefix}[]${suffix}`, path, indent }]
    return [
      { text: `${prefix}[`, path, indent },
      ...value.flatMap((v, i) => lines(v, `${path}[${i}]`, indent + 1, '', i < value.length - 1 ? ',' : '')),
      { text: `]${suffix}`, path, indent },
    ]
  }
  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b))
    if (entries.length === 0) return [{ text: `${prefix}{}${suffix}`, path, indent }]
    return [
      { text: `${prefix}{`, path, indent },
      ...entries.flatMap(([k, v], i) => lines(v, `${path}.${k}`, indent + 1, `${JSON.stringify(k)}: `, i < entries.length - 1 ? ',' : '')),
      { text: `}${suffix}`, path, indent },
    ]
  }
  return [{ text: `${prefix}${JSON.stringify(value)}${suffix}`, path, indent }]
}

/** A line differs when its path is a differing path, or lies inside one. */
const differs = (path: string, diffs: ReadonlySet<string>) => [...diffs].some((d) => path === d || path.startsWith(d + '.') || path.startsWith(d + '['))

function Pane({ label, value, diffs }: { label: string; value: unknown; diffs: ReadonlySet<string> }) {
  return (
    <figure className="flex min-w-0 flex-1 flex-col gap-1">
      <figcaption className="text-xs font-medium text-muted">{label}</figcaption>
      <pre className="overflow-x-auto rounded-md border border-border bg-surface-muted py-2 font-mono text-xs" aria-label={label}>
        {lines(value).map((l, i) => {
          const hit = differs(l.path, diffs)
          return (
            <div key={i} data-path={l.path} data-diff={hit || undefined} className={cn('px-3', hit && 'bg-danger-soft text-danger')} style={{ paddingLeft: 12 + l.indent * 14 }}>
              {l.text}
            </div>
          )
        })}
      </pre>
    </figure>
  )
}

/** Side-by-side JSON (expected vs actual) with the differing paths highlighted (OC-030 AC5). */
export function JsonDiff({ expected, actual, paths }: { expected: unknown; actual: unknown; paths: string[] }) {
  const { t } = useTranslation()
  const diffs = new Set(paths)
  return (
    <div className="flex flex-col gap-3 md:flex-row">
      <Pane label={t('verify.expected')} value={expected} diffs={diffs} />
      <Pane label={t('verify.actual')} value={actual} diffs={diffs} />
    </div>
  )
}
