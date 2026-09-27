import { http, HttpResponse } from 'msw'
import type { ReactElement } from 'react'
import type { MappingPreviewRequest } from '@/features/mapping/api'
import { createWorkbenchStore, WorkbenchContext } from '@/features/mapping/store'
import type { MappingPreview, MappingSpec, MappingStats, MappingView, SourceColumn } from '@/features/mapping/types'
import { server } from './msw'
import { renderUI } from './render'

export const scol = (name: string, jsonType: string, over: Partial<SourceColumn> = {}): SourceColumn => ({
  name, ordinal: 1, dataType: jsonType, udtName: jsonType, nullable: true, default: null, isPrimaryKey: false, isUnique: false,
  maxLength: null, numericPrecision: null, numericScale: null, isArray: false, enumValues: null, jsonType: jsonType as SourceColumn['jsonType'], ...over,
})

export const columns: SourceColumn[] = [
  scol('customer_id', 'integer', { ordinal: 1, isPrimaryKey: true, nullable: false }),
  scol('first_name', 'string', { ordinal: 2 }),
  scol('last_name', 'string', { ordinal: 3 }),
  scol('email_address', 'string', { ordinal: 4 }),
  scol('loyalty_tier', 'string', { ordinal: 5 }),
  scol('created_on', 'datetime', { ordinal: 6 }),
  scol('legacy_flag', 'boolean', { ordinal: 7 }),
  scol('age_text', 'string', { ordinal: 8 }),
]

/** 1 auto (customerId), 1 auto (first), 1 suggested (email), 1 manual (createdAt), ignored legacy_flag. */
export function sampleSpec(): MappingSpec {
  return {
    docSchema: {
      source: 'generated',
      root: {
        id: 'fld_root', name: '$', type: 'object',
        children: [
          { id: 'f_id', name: 'customerId', type: 'integer', required: true },
          { id: 'f_name', name: 'name', type: 'object', children: [{ id: 'f_first', name: 'first', type: 'string' }, { id: 'f_last', name: 'last', type: 'string' }] },
          { id: 'f_email', name: 'email', type: 'string', required: true },
          { id: 'f_created', name: 'createdAt', type: 'integer' },
          { id: 'f_age', name: 'age', type: 'integer' },
          { id: 'f_profile', name: 'profile', type: 'object', children: [] },
        ],
      },
    },
    key: { template: 'customer::{customer_id}' },
    typeField: { enabled: true, name: 'type', value: 'customer' },
    nullHandling: 'include',
    fieldMappings: [
      { id: 'm_id', targetFieldId: 'f_id', sources: [{ column: 'customer_id' }], transforms: [], origin: 'auto', state: 'active', confidence: 0.97, reason: 'normalized-equal' },
      { id: 'm_first', targetFieldId: 'f_first', sources: [{ column: 'first_name' }], transforms: [{ op: 'trim' }], origin: 'auto', state: 'active', confidence: 0.9 },
      { id: 'm_email', targetFieldId: 'f_email', sources: [{ column: 'email_address' }], transforms: [], origin: 'suggested', state: 'pending', confidence: 0.78, reason: 'synonym' },
      { id: 'm_created', targetFieldId: 'f_created', sources: [{ column: 'created_on' }], transforms: [{ op: 'formatDatetime', args: { format: 'epoch_ms' } }], origin: 'manual', state: 'active' },
    ],
    embeds: [],
    ignoredColumns: ['legacy_flag'],
  }
}

export const stats = (over: Partial<MappingStats> = {}): MappingStats => ({
  totalSourceColumns: 23, autoMapped: 10, suggested: 4, manuallyMapped: 3, unmappedSource: 6, ignored: 1,
  totalTargetLeaves: 20, mappedTargetLeaves: 13, requiredTargetUnmapped: 2, autoMapRatio: 10 / 23, coverageRatio: 13 / 23, averageConfidence: 0.94, ...over,
})

export function mappingView(over: Partial<MappingView> = {}): MappingView {
  return {
    id: 'm1', projectId: 'p1', sourceSchema: 'shop', sourceTable: 'customer',
    target: { bucket: 'shop', scope: 'sales', collection: 'customer' },
    spec: sampleSpec(), status: 'draft', automapStats: stats(), stats: stats(), issues: [],
    explain: {
      m_email: { score: 0.78, nameScore: 0.88, typeScore: 1, typeClass: 'exact', reason: "Names are synonyms ('email address' ~ 'email') (78%); types match", signals: [{ name: 'SYNONYM', score: 0.88 }] },
      m_id: { score: 0.97, nameScore: 0.97, typeScore: 1, typeClass: 'exact', reason: 'Names match ignoring case and separators (97%); types match', signals: [{ name: 'NORMALIZED_EQUAL', score: 0.97 }] },
    },
    version: 3, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
    ...over,
  }
}

/** Serves GET/PUT of a mapping from memory; returns the recorded PUT bodies. */
export function mockMapping(view: MappingView = mappingView()) {
  const state = { view, puts: [] as { ifMatch: string | null; body: unknown }[] }
  server.use(
    http.get('*/api/v1/projects/:pid/mappings/:mid', () => HttpResponse.json(state.view, { headers: { ETag: `"${state.view.version}"` } })),
    http.put('*/api/v1/projects/:pid/mappings/:mid', async ({ request }) => {
      const body = (await request.json()) as { spec: MappingSpec }
      state.puts.push({ ifMatch: request.headers.get('If-Match'), body })
      state.view = { ...state.view, spec: body.spec, version: state.view.version + 1 }
      return HttpResponse.json(state.view)
    }),
  )
  return state
}

/** Renders UI inside a fresh workbench store (returned for assertions). */
export function renderWorkbench(ui: ReactElement, view: MappingView = mappingView(), cols: SourceColumn[] = columns) {
  const store = createWorkbenchStore(view, cols)
  const utils = renderUI(<WorkbenchContext.Provider value={store}>{ui}</WorkbenchContext.Provider>)
  return { ...utils, store }
}

/** Serves POST …/preview with the given response and records request bodies. */
export function mockPreview(response: MappingPreview) {
  const bodies: MappingPreviewRequest[] = []
  server.use(
    http.post('*/api/v1/projects/:pid/mappings/:mid/preview', async ({ request }) => {
      bodies.push((await request.json()) as MappingPreviewRequest)
      return HttpResponse.json(response)
    }),
  )
  return bodies
}

/** customer with orders embedded as orders[] (one mapped field) and address still a candidate. */
export function embedView(over: Partial<MappingView> = {}): MappingView {
  const spec = sampleSpec()
  spec.docSchema.root.children = [
    ...(spec.docSchema.root.children ?? []),
    {
      id: 'f_orders', name: 'orders', type: 'array',
      items: { id: 'f_item', name: '[]', type: 'object', children: [{ id: 'f_total', name: 'total', type: 'number' }, { id: 'f_date', name: 'orderDate', type: 'string' }] },
    },
  ]
  spec.embeds = [{ id: 'emb_o', kind: 'one-to-many', table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetFieldId: 'f_orders', limit: 1000 }]
  spec.fieldMappings = [
    ...spec.fieldMappings,
    { id: 'm_total', targetFieldId: 'f_total', sources: [{ column: 'total', embed: 'emb_o' }], transforms: [], origin: 'auto', state: 'active', confidence: 1 },
  ]
  return mappingView({
    spec,
    embedTables: {
      emb_o: {
        table: { schema: 'shop', name: 'orders' },
        columns: [scol('order_id', 'integer', { isPrimaryKey: true }), scol('customer_id', 'integer'), scol('order_date', 'string'), scol('total', 'number')],
      },
    },
    embedCandidates: [
      { kind: 'many-to-one', table: { schema: 'shop', name: 'address' }, join: [{ parent: 'address_id', child: 'address_id' }], constraint: 'customer_address_fk', rowEstimate: 400 },
      { kind: 'one-to-many', table: { schema: 'shop', name: 'orders' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'orders_customer_fk', rowEstimate: 1_200_000, rowsPerParent: 600, embedId: 'emb_o' },
      { kind: 'one-to-many', table: { schema: 'shop', name: 'page_view' }, join: [{ parent: 'customer_id', child: 'customer_id' }], constraint: 'pv_customer_fk', rowEstimate: 5_000_000, rowsPerParent: 2500 },
    ],
    ...over,
  })
}
