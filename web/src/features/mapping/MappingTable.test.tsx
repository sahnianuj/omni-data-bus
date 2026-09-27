import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { describe, expect, it } from 'vitest'
import { columns, renderWorkbench, sampleSpec } from '@/test/mapping'
import { connect, replaceSource } from './mappingOps'
import { MappingTable, setSource } from './MappingTable'

describe('MappingTable', () => {
  it('AC7: keyboard map via combobox', async () => {
    const { store } = renderWorkbench(<MappingTable />)
    const select = screen.getByRole('combobox', { name: 'Source for age' })
    select.focus()
    await userEvent.selectOptions(select, 'age_text')
    const m = store.getState().history.present.fieldMappings.find((x) => x.targetFieldId === 'f_age')!
    expect(m).toMatchObject({ sources: [{ column: 'age_text' }], transforms: [{ op: 'parseInteger' }] })
    // Ignored columns aren't offered.
    expect(within(select).queryByRole('option', { name: 'legacy_flag (boolean)' })).toBeNull()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Source for age' }), '')
    expect(store.getState().history.present.fieldMappings.some((x) => x.targetFieldId === 'f_age')).toBe(false)
  })

  it('AC7: same spec as canvas actions', () => {
    const col = columns.find((c) => c.name === 'age_text')!
    const viaTable = setSource(sampleSpec(), 'f_age', col)
    const viaCanvas = connect(sampleSpec(), col, 'f_age')
    expect(viaCanvas.outcome).toBe('connected')
    const strip = (s: typeof viaTable) => s.fieldMappings.map(({ id: _id, ...m }) => m)
    expect(strip(viaTable)).toEqual(strip((viaCanvas as { spec: typeof viaTable }).spec))
    const last = columns.find((c) => c.name === 'last_name')!
    expect(setSource(sampleSpec(), 'f_first', last)).toEqual(replaceSource(sampleSpec(), 'm_first', last))
  })

  it('AC7: axe clean', async () => {
    renderWorkbench(<MappingTable />)
    const res = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })
    expect(res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical').map((v) => v.id)).toEqual([])
  })

  it('lists targets per source column', async () => {
    renderWorkbench(<MappingTable />)
    await userEvent.click(screen.getByRole('tab', { name: 'By source column' }))
    const row = screen.getByRole('row', { name: /first_name/ })
    expect(row).toHaveTextContent('name.first')
    expect(screen.getByRole('row', { name: /legacy_flag/ })).toHaveTextContent('ignored')
  })
})
