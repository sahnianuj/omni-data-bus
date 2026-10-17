import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { fieldTypeOf } from '@/features/docschema/generate'
import { foreignKeyOf, scopeDepth } from '@/features/composer/bindPrefill'
import { collapseToId, nodeOf, OpError } from '@/features/composer/composerOps'
import { EmbedKeyDialog } from '@/features/composer/EmbedKeyDialog'
import { boundRelations } from '@/features/composer/scope'
import { runAutomapScope } from './api'
import { useTablesHops } from './relationsApi'
import { useWorkbench, useWorkbenchStore } from './store'

const key = (t: { schema: string; name: string }) => `${t.schema}.${t.name}`

/**
 * The Canvas's foreign-key actions (OC-143), as in the Composer (OC-064): "Embed brands here…"
 * on a field holding a foreign key, at any depth (the scope table's own hops, OC-141), and
 * "Collapse to id" on an object embedded many-to-one. Both make the same spec as the Composer.
 */
export function KeyActions({ fieldId }: { fieldId: string }) {
  const { t } = useTranslation()
  const store = useWorkbenchStore()
  const view = useWorkbench((s) => s.view)
  const spec = useWorkbench((s) => s.history.present)
  const columns = useWorkbench((s) => s.columns)
  const apply = useWorkbench((s) => s.apply)
  const replace = useWorkbench((s) => s.replace)
  const select = useWorkbench((s) => s.select)
  const [embedding, setEmbedding] = useState(false)
  const own = { schema: view.sourceSchema, name: view.sourceTable }
  const { hopsOf, maxRelationDepth } = useTablesHops(`${view.projectId}::source`, [key(spec.root?.table ?? own), ...spec.relations.map((r) => key(r.table))])
  const fk = foreignKeyOf(hopsOf, spec, fieldId, own)
  const depth = scopeDepth(spec, fieldId)
  const blocked = maxRelationDepth != null && depth >= maxRelationDepth ? t('embed.depthLimit', { depth, max: maxRelationDepth }) : undefined
  const bound = boundRelations(spec).get(fieldId)
  const collapsible = !!bound && !bound.group && bound.kind === 'many-to-one' && bound.join?.length === 1 && nodeOf(spec, fieldId)?.type === 'object'
  if (!fk && !collapsible) return null

  const collapse = () => {
    const col = bound?.parentId ? undefined : columns.find((c) => c.name === bound?.join?.[0]?.parent)
    try {
      apply((s) => collapseToId(s, fieldId, col ? { type: fieldTypeOf(col).type } : {}))
    } catch (e) {
      if (e instanceof OpError) toast.error(e.message)
      else throw e
    }
  }
  const embedded = async (objectId: string) => {
    setEmbedding(false)
    select({ kind: 'field', id: objectId })
    try {
      await store.getState().flush()
      const s = store.getState()
      replace(await runAutomapScope(view.projectId, s.view.id, s.version, { nodeId: objectId }))
    } catch {
      // the object is embedded; mapping its fields can be done by hand
    }
  }

  return (
    <>
      {fk ? (
        <Button size="sm" variant="secondary" disabled={!!blocked} title={blocked} onClick={() => setEmbedding(true)}>
          {t('composer.embedHere', { table: fk.table.name })}
        </Button>
      ) : null}
      {collapsible ? (
        <Button size="sm" variant="ghost" onClick={collapse}>
          {t('composer.collapseToId')}
        </Button>
      ) : null}
      {embedding && fk ? <EmbedKeyDialog fieldId={fieldId} fk={fk} onClose={() => setEmbedding(false)} onDone={(id) => void embedded(id)} /> : null}
    </>
  )
}
