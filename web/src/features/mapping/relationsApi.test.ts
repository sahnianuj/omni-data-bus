import { screen, waitFor } from '@testing-library/react'
import { createElement } from 'react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, setupMswServer } from '@/test/msw'
import { renderUI } from '@/test/render'
import { useRelationHops, useTablesHops } from './relationsApi'

setupMswServer()

function Probe() {
  const one = useRelationHops('p1::source', 'sales.products')
  const many = useTablesHops('p1::source', ['sales.products', 'sales.products', 'sales.brands'])
  const products = many.hopsOf({ schema: 'sales', name: 'products' })
  return createElement('p', null, `${one.data?.hops.length ?? '-'} ${products?.length ?? '-'} ${many.maxRelationDepth ?? '-'}`)
}

describe('relationsApi', () => {
  it('OC-141 AC4: hops cached per table', async () => {
    const requested: string[] = []
    server.use(
      http.get('*/api/v1/connections/:id/relations/hops', ({ request }) => {
        const table = new URL(request.url).searchParams.get('table')!
        requested.push(table)
        const [schema, name] = table.split('.')
        return HttpResponse.json({
          table: { schema, name }, junctions: [], advice: [], maxRelationDepth: 5,
          hops: name === 'products' ? [{ from: { schema, name }, to: { schema, name: 'brands' }, kind: 'many-to-one', join: [{ parent: 'brand_id', child: 'id' }], indexed: true }] : [],
        })
      }),
    )
    renderUI(createElement(Probe))
    await waitFor(() => expect(screen.getByText('1 1 5')).toBeInTheDocument())
    expect(requested.sort()).toEqual(['sales.brands', 'sales.products'])
  })
})
