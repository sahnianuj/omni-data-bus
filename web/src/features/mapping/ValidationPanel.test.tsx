import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { findField } from '@/features/docschema/docschemaOps'
import { embedView, mappingView, renderWorkbench, sampleSpec } from '@/test/mapping'
import type { MappingIssue } from './types'
import { highlightPath, ValidationPanel } from './ValidationPanel'

const issues: MappingIssue[] = [
  { severity: 'warning', code: 'SUGGESTIONS_PENDING', path: 'fieldMappings', message: '1 suggestion(s) are waiting for review.', fix: { kind: 'accept-suggestions', label: 'Accept all suggestions' } },
  { severity: 'error', code: 'REQUIRED_FIELD_UNMAPPED', path: 'docSchema.email', fieldId: 'f_email', message: 'Required field email has no source.', fix: { kind: 'mark-optional', label: 'Mark optional' } },
  { severity: 'error', code: 'TRANSFORM_INVALID', path: 'fieldMappings.m_first', fieldMappingId: 'm_first', message: 'Bad regex.' },
  { severity: 'error', code: 'TARGET_KEYSPACE_MISSING', path: 'target', message: 'Collection missing.', fix: { kind: 'enable-create', label: 'Create when migration starts' } },
]

describe('ValidationPanel', () => {
  it('AC2: groups and go-to', async () => {
    const { store } = renderWorkbench(<ValidationPanel />, mappingView({ issues }))
    const errors = screen.getByRole('region', { name: '3 errors' })
    const warnings = screen.getByRole('region', { name: '1 warning' })
    expect(within(errors).getAllByRole('listitem')).toHaveLength(3)
    expect(within(warnings).getByText(/waiting for review/)).toBeInTheDocument()

    const regex = within(errors).getByText('Bad regex.').closest('li')!
    await userEvent.click(within(regex).getByRole('button', { name: 'Go to' }))
    expect(store.getState().selection).toEqual({ kind: 'mapping', id: 'm_first' })

    const email = within(errors).getByRole('button', { name: /Required field email/ }).closest('li')!
    await userEvent.click(within(email).getByRole('button', { name: 'Go to' }))
    expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_email' })
    expect(store.getState().mode).toBe('design')
    // Without a handler the target fix isn't offered.
    expect(screen.queryByRole('button', { name: 'Create when migration starts' })).not.toBeInTheDocument()
  })

  it('AC2: quick fix mutates store', async () => {
    const onEnableCreate = vi.fn()
    const { store } = renderWorkbench(<ValidationPanel onEnableCreate={onEnableCreate} />, mappingView({ issues }))
    await userEvent.click(screen.getByRole('button', { name: 'Mark optional' }))
    expect(findField(store.getState().history.present.docSchema, 'f_email')?.field.required).toBeFalsy()
    await userEvent.click(screen.getByRole('button', { name: 'Accept all suggestions' }))
    expect(store.getState().history.present.fieldMappings.find((m) => m.id === 'm_email')?.state).toBe('active')
    await userEvent.click(screen.getByRole('button', { name: 'Create when migration starts' }))
    expect(onEnableCreate).toHaveBeenCalledOnce()
    expect(store.getState().revision).toBe(2)
  })

  it('shows a ready message without issues', () => {
    renderWorkbench(<ValidationPanel />)
    expect(screen.getByText('No issues. This mapping is ready.')).toBeInTheDocument()
  })

  it('OC-070 AC3: remove and add all', async () => {
    const view = embedView()
    view.spec = { ...view.spec, docSchema: { ...view.spec.docSchema, imported: { at: '2026-10-01T00:00:00Z', format: 'sample', samples: 1, fieldCount: 0, fields: [] } } }
    const extra = (path: string, fieldId: string): MappingIssue => ({
      severity: 'info', code: 'REFERENCE_EXTRA_FIELD', path, nodePath: path, fieldId, message: `${path} isn't in the design you imported.`, fix: { kind: 'add-to-design', label: 'Add to design' },
    })
    const email = view.spec.docSchema.root.children!.find((c) => c.name === 'email')!
    view.issues = [extra('orders', 'f_orders'), extra('email', email.id)]
    const { store } = renderWorkbench(<ValidationPanel />, view)
    await userEvent.click(screen.getByRole('button', { name: 'Remove orders' }))
    expect(store.getState().history.present.relations).toEqual([])
    expect(findField(store.getState().history.present.docSchema, 'f_orders')).toBeUndefined()

    await userEvent.click(screen.getByRole('button', { name: 'Add all 2 to design' }))
    expect(store.getState().history.present.docSchema.imported!.fields!.map((f) => f.path)).toEqual(['email'])
  })
  it('OC-176 AC1: the field name is green; messages without it are unchanged', () => {
    const version: MappingIssue = { severity: 'error', code: 'REQUIRED_FIELD_UNMAPPED', path: 'version', fieldId: 'f_age', message: 'Required field version has no mapping. Map a source, set a constant, or mark it optional.' }
    renderWorkbench(<ValidationPanel />, mappingView({ issues: [version, ...issues] }))
    const name = screen.getByText('version', { selector: 'span' })
    expect(name).toHaveClass('text-success', 'font-mono')
    expect(name.closest('button')).toHaveTextContent('Required field version has no mapping. Map a source, set a constant, or mark it optional.')
    expect(screen.getByText('Collection missing.').querySelector('.text-success')).toBeNull()
    // A whole word only.
    expect(highlightPath('Field types differ', 'type')).toBeNull()
    expect(highlightPath('Required field pricing.total has no mapping.', 'pricing.total')).toEqual(['Required field ', 'pricing.total', ' has no mapping.'])
  })

  it('OC-176 AC2: clicking the issue reveals its field in the Canvas', async () => {
    const issue: MappingIssue = { severity: 'error', code: 'REQUIRED_FIELD_UNMAPPED', path: 'name.last', fieldId: 'f_last', message: 'Required field name.last has no mapping.' }
    const { store } = renderWorkbench(<ValidationPanel />, mappingView({ issues: [issue] }))
    store.setState({ mode: 'table', filter: 'manual', search: 'zz' })
    store.getState().toggleCollapsed('f_name')
    await userEvent.click(screen.getByRole('button', { name: 'Required field name.last has no mapping.' }))
    const s = store.getState()
    expect(s.mode).toBe('canvas')
    expect(s.selection).toEqual({ kind: 'field', id: 'f_last' })
    expect(s.collapsed.has('f_name')).toBe(false)
    expect([s.filter, s.search]).toEqual(['all', ''])
    expect(s.reveal).toEqual({ id: 'f_last', seq: 1 })
    await userEvent.click(screen.getByRole('button', { name: 'Required field name.last has no mapping.' }))
    expect(store.getState().reveal?.seq).toBe(2)
  })
  it('a field filled twice offers to keep either mapping', async () => {
    const spec = sampleSpec()
    spec.fieldMappings.push({ id: 'map_762e0cf161', targetFieldId: 'f_first', sources: [{ column: 'last_name' }], transforms: [], origin: 'manual', state: 'active' })
    const dup: MappingIssue = { severity: 'error', code: 'DUPLICATE_TARGET', path: 'name.first', fieldId: 'f_first', fieldMappingId: 'map_762e0cf161', message: 'Field name.first is filled twice: from customer.first_name and from customer.last_name. Keep one.' }
    const { store } = renderWorkbench(<ValidationPanel />, mappingView({ spec, issues: [dup] }))
    expect(screen.getByRole('button', { name: 'Keep from customer.first_name' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Keep from customer.last_name' }))
    const left = store.getState().history.present.fieldMappings.filter((m) => m.targetFieldId === 'f_first')
    expect(left.map((m) => m.id)).toEqual(['map_762e0cf161'])
  })
  it('OC-176: a lookup issue shows its table in green; a place like "source" stays plain', () => {
    const spec = sampleSpec()
    spec.relations.push({ id: 'rel_lk', parentId: null, kind: 'many-to-one', lookupOnly: true, table: { schema: 'pos', name: 'partners' }, join: [{ parent: 'customer_id', child: 'id' }] })
    const found: MappingIssue[] = [
      { severity: 'info', code: 'LOOKUP_UNUSED', path: 'relations.rel_lk', message: "Lookup partners (values only) isn't read by any field. Remove it, or map a field from it.", fix: { kind: 'remove-relation', label: 'Remove lookup', relationId: 'rel_lk' } },
      { severity: 'info', code: 'UNMAPPED_SOURCE_COLUMNS', path: 'source', message: "10 source column(s) aren't mapped and won't be migrated." },
    ]
    renderWorkbench(<ValidationPanel />, mappingView({ spec, issues: found }))
    expect(screen.getByText('partners', { selector: 'span' })).toHaveClass('text-success')
    expect(screen.getByText(/10 source column/).querySelector('.text-success')).toBeNull()
  })
})
