import { http, HttpResponse } from 'msw'
import type { Snapshot } from '@/features/discovery/api'
import type { MappingSpec, MappingView } from '@/features/mapping/types'
import { col, mockSchema, tbl } from './discovery'
import { mappingView, mockMapping, ordersGraph, sampleSpec } from './mapping'
import { server } from './msw'

// The composer's fixtures (OC-048/049): the public.orders schema of ordersGraph() and a composed
// order document over it.

const s = (name: string, ordinal: number) => col(name, ordinal, { dataType: 'text', udtName: 'text', jsonType: 'string', numericPrecision: null, numericScale: null })

/** public: orders, order_items, products, customers, tags, order_tags, audit_log (unrelated). */
export function ordersSnapshot(): Snapshot {
  return {
    snapshotId: 'snap-o', capturedAt: new Date().toISOString(), durationMs: 50, tableCount: 7, database: 'shop', serverVersion: '16.3',
    schemas: [{
      name: 'public',
      tables: [
        tbl('orders', { columns: [col('order_id', 1, { isPrimaryKey: true }), s('order_no', 2), col('customer_id', 3), s('cust_code', 4)], primaryKey: ['order_id'] }),
        tbl('order_items', { rowEstimate: 3000, columns: [col('order_id', 1), col('line_no', 2), col('product_id', 3), col('quantity', 4), col('unit_price', 5, { jsonType: 'number', udtName: 'numeric' })], primaryKey: ['order_id', 'line_no'] }),
        tbl('products', { rowEstimate: 50, columns: [col('product_id', 1, { isPrimaryKey: true }), s('sku', 2), s('name', 3)], primaryKey: ['product_id'] }),
        tbl('customers', { columns: [col('customer_id', 1, { isPrimaryKey: true }), s('name', 2), s('code', 3)], primaryKey: ['customer_id'] }),
        tbl('tags', { columns: [col('tag_id', 1, { isPrimaryKey: true }), s('label', 2)], primaryKey: ['tag_id'] }),
        tbl('order_tags', { columns: [col('order_id', 1), col('tag_id', 2), s('tagged_at', 3)], primaryKey: ['order_id', 'tag_id'] }),
        tbl('audit_log', { columns: [col('id', 1, { isPrimaryKey: true })] }),
      ],
    }],
  }
}

export const ordersColumns = () => ordersSnapshot().schemas[0]!.tables[0]!.columns

/**
 * A composed order: orderId, customer{name} (many-to-one), items[] from order_items with
 * lineNo, sku from a products lookup, and product{name} bound to products; tags[] many-to-many
 * via order_tags. Items carry a transform and a count aggregate sits at the root.
 */
export function composedSpec(): MappingSpec {
  return {
    specVersion: 2,
    mode: 'composed',
    root: { table: { schema: 'public', name: 'orders' } },
    docSchema: {
      source: 'manual',
      root: {
        id: 'fld_root', name: '$', type: 'object',
        children: [
          { id: 'f_oid', name: 'orderId', type: 'integer' },
          { id: 'f_count', name: 'itemCount', type: 'integer' },
          { id: 'f_cust', name: 'customer', type: 'object', binding: { relationId: 'rel_cust' }, children: [{ id: 'f_cname', name: 'name', type: 'string' }] },
          {
            id: 'f_items', name: 'items', type: 'array', binding: { relationId: 'rel_items' },
            items: {
              id: 'f_item', name: '[]', type: 'object',
              children: [
                { id: 'f_line', name: 'lineNo', type: 'integer' },
                { id: 'f_sku', name: 'sku', type: 'string' },
                { id: 'f_product', name: 'product', type: 'object', binding: { relationId: 'rel_prod' }, children: [{ id: 'f_pname', name: 'name', type: 'string' }] },
              ],
            },
          },
          { id: 'f_tags', name: 'tags', type: 'array', binding: { relationId: 'rel_tags' }, items: { id: 'f_tag', name: '[]', type: 'object', children: [{ id: 'f_label', name: 'label', type: 'string' }] } },
        ],
      },
    },
    key: { template: 'order::{order_id}' },
    typeField: { enabled: false, name: '', value: '' },
    nullHandling: 'include',
    relations: [
      { id: 'rel_cust', parentId: null, kind: 'many-to-one', table: { schema: 'public', name: 'customers' }, join: [{ parent: 'customer_id', child: 'customer_id' }], targetFieldId: 'f_cust' },
      { id: 'rel_items', parentId: null, kind: 'one-to-many', table: { schema: 'public', name: 'order_items' }, join: [{ parent: 'order_id', child: 'order_id' }], targetFieldId: 'f_items', orderBy: [{ column: 'line_no' }], limit: 1000 },
      { id: 'rel_plookup', parentId: 'rel_items', kind: 'many-to-one', lookupOnly: true, table: { schema: 'public', name: 'products' }, join: [{ parent: 'product_id', child: 'product_id' }] },
      { id: 'rel_prod', parentId: 'rel_items', kind: 'many-to-one', table: { schema: 'public', name: 'products' }, join: [{ parent: 'product_id', child: 'product_id' }], targetFieldId: 'f_product' },
      {
        id: 'rel_tags', parentId: null, kind: 'many-to-many', table: { schema: 'public', name: 'tags' }, targetFieldId: 'f_tags',
        via: { table: { schema: 'public', name: 'order_tags' }, parentJoin: [{ parent: 'order_id', via: 'order_id' }], childJoin: [{ via: 'tag_id', child: 'tag_id' }], columns: ['tagged_at'] },
      },
    ],
    aggregates: [{ id: 'agg_n', relationId: 'rel_items', fn: 'count', targetFieldId: 'f_count' }],
    fieldMappings: [
      { id: 'm_oid', targetFieldId: 'f_oid', sources: [{ column: 'order_id' }], transforms: [], origin: 'auto', state: 'active', confidence: 1 },
      { id: 'm_cname', targetFieldId: 'f_cname', sources: [{ relation: 'rel_cust', column: 'name' }], transforms: [{ op: 'trim' }], origin: 'manual', state: 'active' },
      { id: 'm_line', targetFieldId: 'f_line', sources: [{ relation: 'rel_items', column: 'line_no' }], transforms: [], origin: 'auto', state: 'active', confidence: 0.95 },
      { id: 'm_sku', targetFieldId: 'f_sku', sources: [{ relation: 'rel_plookup', column: 'sku' }], transforms: [{ op: 'upper' }], origin: 'manual', state: 'active' },
      { id: 'm_pname', targetFieldId: 'f_pname', sources: [{ relation: 'rel_prod', column: 'name' }], transforms: [], origin: 'auto', state: 'active', confidence: 0.9 },
      { id: 'm_label', targetFieldId: 'f_label', sources: [{ relation: 'rel_tags', column: 'label' }], transforms: [], origin: 'auto', state: 'active', confidence: 0.9 },
    ],
    ignoredColumns: [],
  }
}

/** A composed mapping of public.orders. */
export function composedView(over: Partial<MappingView> = {}): MappingView {
  return mappingView({ sourceSchema: 'public', sourceTable: 'orders', spec: composedSpec(), embedCandidates: [], embedTables: {}, issues: [], ...over })
}

/** Serves the orders relationship graph, schema, table details and an empty preview (records preview bodies). */
export function mockComposer() {
  const snap = ordersSnapshot()
  mockSchema(snap)
  const previews: unknown[] = []
  server.use(
    http.get('*/api/v1/connections/:id/relations', () => HttpResponse.json(ordersGraph())),
    http.get('*/api/v1/connections/:id/schema/:schema/:table', ({ params }) => {
      const t = snap.schemas[0]!.tables.find((x) => x.name === params.table)
      return t ? HttpResponse.json(t) : HttpResponse.json({ status: 404, code: 'NOT_FOUND', title: 'Not found' }, { status: 404 })
    }),
    http.post('*/api/v1/projects/:pid/mappings/:mid/preview', async ({ request }) => {
      previews.push(await request.json())
      return HttpResponse.json({ writeMode: 'upsert', items: [], summary: { ok: 0, errors: 0, avgSizeBytes: 0, maxSizeBytes: 0, collisions: 0 } })
    }),
  )
  return { previews }
}

/** The public.orders mapping on the canvas with an empty design (served from memory; returns mockMapping's state). */
export function mockOrdersMapping() {
  const spec = sampleSpec()
  spec.docSchema.root.children = []
  spec.fieldMappings = []
  spec.ignoredColumns = []
  spec.key = { template: 'order::{order_id}' }
  spec.typeField = { enabled: false, name: '', value: '' }
  const state = mockMapping(mappingView({ sourceSchema: 'public', sourceTable: 'orders', spec, embedTables: {}, embedCandidates: [], issues: [] }))
  // Scoped auto-map answers with the saved mapping (the mapping is already what the test built).
  server.use(http.post('*/api/v1/projects/:pid/mappings/:mid/automap', () => HttpResponse.json(state.view)))
  return state
}
