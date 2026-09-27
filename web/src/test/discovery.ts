import { http, HttpResponse } from 'msw'
import type { PreviewResult, Snapshot, SourceColumn, SourceTable } from '@/features/discovery/api'
import { server } from './msw'

export const col = (name: string, ordinal: number, over: Partial<SourceColumn> = {}): SourceColumn => ({
  name,
  ordinal,
  dataType: 'integer',
  udtName: 'int4',
  nullable: false,
  default: null,
  isPrimaryKey: false,
  isUnique: false,
  maxLength: null,
  numericPrecision: 32,
  numericScale: 0,
  isArray: false,
  enumValues: null,
  jsonType: 'integer',
  ...over,
})

export const tbl = (name: string, over: Partial<SourceTable> = {}): SourceTable => ({
  name,
  kind: 'table',
  rowEstimate: 1000,
  sizeBytes: 81920,
  readable: true,
  columns: [col('id', 1, { isPrimaryKey: true, isUnique: true })],
  primaryKey: ['id'],
  uniqueConstraints: [],
  foreignKeys: [],
  referencedBy: [],
  indexes: [],
  ...over,
})

/** A small shop: address ← customer ← orders ← "Order Details", a view, a table without a PK, and billing (unreadable). */
export function shopSnapshot(over: Partial<Snapshot> = {}): Snapshot {
  const customer = tbl('customer', {
    rowEstimate: 120_345,
    sizeBytes: 30_932_992,
    comment: 'People who buy',
    columns: [
      col('customer_id', 1, { isPrimaryKey: true, isUnique: true }),
      col('email_address', 2, { dataType: 'citext', udtName: 'citext', jsonType: 'string', isUnique: true, numericPrecision: null, numericScale: null }),
      col('address_id', 3, { nullable: true }),
      col('search_vector', 4, { dataType: 'tsvector', udtName: 'tsvector', jsonType: 'string', warning: 'UNSUPPORTED_TYPE_AS_TEXT', nullable: true }),
      col('created_on', 5, { dataType: 'timestamp with time zone', udtName: 'timestamptz', jsonType: 'datetime', default: 'now()' }),
    ],
    primaryKey: ['customer_id'],
    uniqueConstraints: [['email_address']],
    foreignKeys: [{ name: 'customer_address_fk', columns: ['address_id'], refSchema: 'shop', refTable: 'address', refColumns: ['address_id'] }],
    referencedBy: [{ schema: 'shop', table: 'orders', name: 'orders_customer_fk', columns: ['customer_id'], refColumns: ['customer_id'] }],
    indexes: [
      { name: 'customer_pkey', columns: ['customer_id'], unique: true, primary: true },
      { name: 'customer_email_key', columns: ['email_address'], unique: true },
    ],
  })
  const orders = tbl('orders', {
    rowEstimate: 1_200_000,
    columns: [col('order_id', 1, { isPrimaryKey: true, dataType: 'bigint', udtName: 'int8' }), col('customer_id', 2)],
    primaryKey: ['order_id'],
    foreignKeys: [{ name: 'orders_customer_fk', columns: ['customer_id'], refSchema: 'shop', refTable: 'customer', refColumns: ['customer_id'] }],
  })
  return {
    snapshotId: 'snap-1',
    capturedAt: new Date(Date.now() - 3 * 60_000).toISOString(),
    durationMs: 120,
    tableCount: 7,
    database: 'shop',
    serverVersion: '16.3',
    schemas: [
      {
        name: 'shop',
        tables: [
          orders,
          customer,
          tbl('address', { rowEstimate: 40_000, columns: [col('address_id', 1, { isPrimaryKey: true })], primaryKey: ['address_id'], referencedBy: [{ schema: 'shop', table: 'customer', name: 'customer_address_fk', columns: ['address_id'], refColumns: ['address_id'] }] }),
          tbl('Order Details', { primaryKey: ['order_id', 'line_no'], columns: [col('order_id', 1, { isPrimaryKey: true }), col('line_no', 2, { isPrimaryKey: true })] }),
          tbl('page_view', { primaryKey: [], columns: [col('viewed_at', 1)], rowEstimate: null, statsStale: true }),
          tbl('v_active_customers', { kind: 'view', rowEstimate: null, primaryKey: [], sizeBytes: 0 }),
        ],
      },
      { name: 'billing', tables: [tbl('invoice', { readable: false })] },
    ],
    ...over,
  }
}

export function mockSchema(snapshot: Snapshot | null, connId = 'p1::source') {
  server.use(
    http.get('*/api/v1/connections/:id/schema', ({ params }) => {
      if (params.id !== connId) return HttpResponse.json({ status: 404, code: 'NOT_FOUND', title: 'Not found' }, { status: 404 })
      if (!snapshot) return HttpResponse.json({ status: 404, code: 'SCHEMA_NOT_DISCOVERED', title: 'Schema not discovered yet' }, { status: 404 })
      return HttpResponse.json(snapshot)
    }),
  )
}

export const previewResult = (over: Partial<PreviewResult> = {}): PreviewResult => ({
  schema: 'shop',
  table: 'customer',
  snapshotId: 'snap-1',
  columns: [
    { name: 'customer_id', dataType: 'integer', jsonType: 'integer' },
    { name: 'preferences', dataType: 'jsonb', jsonType: 'any' },
    { name: 'notes', dataType: 'text', jsonType: 'string' },
  ],
  orderBy: ['customer_id'],
  limit: 50,
  rows: [
    [
      { v: 1, t: 'integer' },
      { v: { newsletter: true, language: 'en' }, t: 'any' },
      { v: null, t: 'string' },
    ],
    [
      { v: 2, t: 'integer' },
      { v: null, t: 'any' },
      { v: 'x'.repeat(1500), t: 'string', truncated: true },
    ],
  ],
  ...over,
})
