import { expect, test } from 'vitest'
import { buildEdges, parentMap } from './buildEdges'
import { largeSpec } from './buildEdges.fixture'

const spec = largeSpec()
const parents = parentMap(spec)
const collapsed = new Set(['g3', 'g7', 'g20'])

// `npm run bench`. The same budget also runs as a plain unit test in buildEdges.test.ts.
test('AC10: 300 edges < 5ms', async ({ bench }) => {
  const r = await bench('buildEdges 300 mappings / 500 leaves', () => {
    buildEdges(spec, collapsed, parents)
  }).run()
  expect(r.latency.p50).toBeLessThan(5)
})
