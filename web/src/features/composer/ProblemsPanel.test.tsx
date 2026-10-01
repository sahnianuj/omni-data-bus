import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import type { MappingIssue, MappingSpec } from '@/features/mapping/types'
import { composedSpec, composedView, mockComposer } from '@/test/composer'
import { renderWorkbench } from '@/test/mapping'
import { setupMswServer } from '@/test/msw'
import { mockSession } from '@/test/session'
import { DocumentTree } from './DocumentTree'
import { ProblemsPanel } from './ProblemsPanel'

setupMswServer()
beforeEach(() => {
  mockSession('owner')
  mockComposer()
})

const issues: MappingIssue[] = [
  { severity: 'warning', code: 'ARRAY_UNBOUND_EMPTY', path: 'tags', nodePath: 'tags', message: 'tags is always empty.' },
  {
    severity: 'error', code: 'SOURCE_OUT_OF_SCOPE', path: 'items[].sku', nodePath: 'items[].sku', fieldId: 'f_sku',
    message: 'items[].sku reads customers.name, but customers isn’t bound on the path to this field; bind it under items or add a lookup.',
  },
  { severity: 'error', code: 'BINDING_KIND_MISMATCH', path: 'customer', nodePath: 'customer', relationId: 'rel_cust', message: 'customer is an object, but order_items is one-to-many.' },
]

describe('ProblemsPanel', () => {
  it('AC3: click selects node', async () => {
    const { store } = renderWorkbench(
      <>
        <DocumentTree onBindTable={() => {}} onDropTable={() => {}} onDropColumn={() => {}} onMapColumn={() => {}} onPaste={() => {}} />
        <ProblemsPanel />
      </>,
      composedView({ issues }),
    )
    const list = screen.getByRole('region', { name: 'Problems (3)' })
    const buttons = list.querySelectorAll('li button')
    // Errors first.
    expect(buttons[0]).toHaveTextContent('SOURCE_OUT_OF_SCOPE')
    expect(buttons[2]).toHaveTextContent('ARRAY_UNBOUND_EMPTY')

    // Both errors put ⛔ on their nodes in the tree.
    expect(screen.getByRole('treeitem', { name: /^sku,.*1 error/ })).toBeInTheDocument()
    expect(screen.getByRole('treeitem', { name: /^customer,.*1 error/ })).toBeInTheDocument()
    expect(screen.getByRole('treeitem', { name: /^tags,.*1 warning/ })).toBeInTheDocument()

    // Clicking a problem selects its node — by field id, or by node path.
    await userEvent.click(buttons[1] as HTMLElement)
    expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_cust' })
    await userEvent.click(buttons[0] as HTMLElement)
    expect(store.getState().selection).toEqual({ kind: 'field', id: 'f_sku' })
    expect(screen.getByRole('treeitem', { name: /^sku,/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('AC4: apply constant hint', async () => {
    const spec: MappingSpec = composedSpec()
    spec.docSchema.root.children!.push({ id: 'f_type', name: 'type', type: 'string', example: 'order' }, { id: 'f_ver', name: 'schemaVersion', type: 'integer', example: 2 })
    spec.docSchema.constantHints = [
      { fieldId: 'f_type', value: 'order', reason: 'discriminator-name' },
      { fieldId: 'f_ver', value: 2, reason: 'same-in-every-sample' },
      { fieldId: 'f_gone', value: 'x', reason: 'schema-const' }, // its field was deleted: not shown
    ]
    const { store } = renderWorkbench(<ProblemsPanel />, composedView({ spec }))
    const hints = screen.getByRole('region', { name: 'Suggested constants (2)' })
    expect(hints).toHaveTextContent('type · a type or version field')
    expect(hints).toHaveTextContent('type is always "order".')

    await userEvent.click(screen.getByRole('button', { name: 'Keep type as the constant "order"' }))
    const next = store.getState().history.present
    expect(next.fieldMappings.find((m) => m.targetFieldId === 'f_type')).toMatchObject({ sources: [], transforms: [{ op: 'constant', args: { value: 'order' } }], state: 'active' })
    expect(next.docSchema.constantHints!.map((h) => h.fieldId)).toEqual(['f_ver', 'f_gone'])

    await userEvent.click(screen.getByRole('button', { name: 'Dismiss the suggestion for schemaVersion' }))
    expect(screen.queryByRole('region', { name: /Suggested constants/ })).not.toBeInTheDocument()
    expect(store.getState().history.present.fieldMappings.some((m) => m.targetFieldId === 'f_ver')).toBe(false)
  })

  it('collapses to one line', async () => {
    renderWorkbench(<ProblemsPanel />, composedView({ issues }))
    const toggle = screen.getByRole('button', { name: 'Problems (3)' })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText(/SOURCE_OUT_OF_SCOPE/)).not.toBeVisible()
  })

  it('OC-065 AC2: repoint fix', async () => {
    // products[].lineNo reads the order's first row: "Read it from each row" re-points it.
    const spec: MappingSpec = composedSpec()
    spec.relations = spec.relations.map((r) => (r.id === 'rel_items' ? { ...r, group: true } : r))
    spec.fieldMappings = spec.fieldMappings.map((m) => (m.id === 'm_line' ? { ...m, sources: [{ column: 'line_no' }] } : m))
    const issue: MappingIssue = {
      severity: 'error', code: 'ITEM_READS_PARENT_ROW', path: 'items[].lineNo', fieldId: 'f_line', fieldMappingId: 'm_line', relationId: 'rel_items',
      message: 'items[].lineNo reads line_no of the parent row, so every item repeats the same value; read it from each row instead.',
      fix: { kind: 'repoint-source', label: 'Read it from each row', relationId: 'rel_items' },
    }
    const { store } = renderWorkbench(<ProblemsPanel />, composedView({ spec, issues: [issue] }))
    await userEvent.click(screen.getByRole('button', { name: 'Read it from each row: items[].lineNo' }))
    expect(store.getState().history.present.fieldMappings.find((m) => m.id === 'm_line')!.sources).toEqual([{ column: 'line_no', relation: 'rel_items' }])
  })

  it('OC-075 AC2: info issues aren\'t listed', () => {
    // An unmapped field of the customer's design is info: no problem, no warning icon in the tree.
    const unmapped: MappingIssue = {
      severity: 'info', code: 'REFERENCE_FIELD_UNMAPPED', path: 'items[].productId', nodePath: 'items[].productId',
      message: 'items[].productId is in the design you imported but missing here.',
      fix: { kind: 'restore-field', label: 'Add productId back (from product_id)', relationId: 'rel_items', columns: ['product_id'] },
    }
    renderWorkbench(
      <>
        <DocumentTree onBindTable={() => {}} onDropTable={() => {}} onDropColumn={() => {}} onMapColumn={() => {}} onPaste={() => {}} />
        <ProblemsPanel />
      </>,
      composedView({ issues: [unmapped] }),
    )
    expect(screen.getByRole('status')).toHaveTextContent('No problems.')
    expect(screen.queryByRole('treeitem', { name: /warning/ })).not.toBeInTheDocument()
  })

  it('says so when there are none', () => {
    renderWorkbench(<ProblemsPanel />, composedView())
    expect(screen.getByRole('status')).toHaveTextContent('No problems.')
  })
})
