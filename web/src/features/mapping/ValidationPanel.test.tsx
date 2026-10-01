import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { findField } from '@/features/docschema/docschemaOps'
import { embedView, mappingView, renderWorkbench } from '@/test/mapping'
import type { MappingIssue } from './types'
import { ValidationPanel } from './ValidationPanel'

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

    const email = within(errors).getByText(/Required field email/).closest('li')!
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
})
