import type { DocField, MappingSpec } from './types'

/** OC-019 AC10 scale: 500 target leaves in 50 objects, 300 mappings (every 4th a 2-source concat). */
export function largeSpec(): MappingSpec {
  const groups: DocField[] = Array.from({ length: 50 }, (_, g) => ({
    id: `g${g}`, name: `group${g}`, type: 'object',
    children: Array.from({ length: 10 }, (_, i) => ({ id: `f${g * 10 + i}`, name: `field${i}`, type: 'string' as const })),
  }))
  return {
    docSchema: { source: 'manual', root: { id: 'fld_root', name: '$', type: 'object', children: groups } },
    key: { template: 'x::{col_0}' },
    typeField: { enabled: false, name: 'type', value: '' },
    nullHandling: 'include',
    fieldMappings: Array.from({ length: 300 }, (_, i) => ({
      id: `m${i}`, targetFieldId: `f${i}`,
      sources: i % 4 === 0 ? [{ column: `col_${i}` }, { column: `col_${(i + 1) % 300}` }] : [{ column: `col_${i}` }],
      transforms: i % 3 === 0 ? [{ op: 'trim' }] : [],
      origin: i % 5 === 0 ? 'manual' : 'auto', state: i % 7 === 0 ? 'pending' : 'active',
    })),
    embeds: [],
    ignoredColumns: [],
  }
}
