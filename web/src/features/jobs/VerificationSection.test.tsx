import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderUI } from '@/test/render'
import { mappingReport, report, verification } from '@/test/report'
import { VerificationSection } from './VerificationSection'

const mismatchSample = {
  checked: 100, matched: 98, mismatched: 1, missing: 1, sourceChanged: 0, missingKeys: ['orders::7'],
  mismatches: [{ key: 'orders::3', expected: '{"id":3,"total":12.5}', actual: '{"id":3,"total":12}', diffs: [{ path: '$.total', expected: 12.5, actual: 12 }] }],
}

describe('VerificationSection', () => {
  it('AC5: statuses', async () => {
    renderUI(
      <VerificationSection
        verifies
        report={report([
          mappingReport('m1', 'customer', verification()),
          mappingReport('m2', 'orders', verification({ mappingId: 'm2', result: 'failed', sample: mismatchSample })),
          mappingReport('m3', 'items', verification({ mappingId: 'm3', result: 'passed_with_warnings', counts: { ...verification().counts, reconciled: false, messages: ['2 keys collided'] } })),
          mappingReport('m4', 'notes'),
        ])}
      />,
    )
    const ok = await screen.findByTestId('verify-m1')
    expect(ok).toHaveTextContent('Passed')
    expect(ok).toHaveTextContent('100/100 samples match · counts reconcile')
    const bad = screen.getByTestId('verify-m2')
    expect(bad).toHaveTextContent('Failed')
    expect(bad).toHaveTextContent('98/100 samples match · 1 mismatched · 1 missing')
    const warn = screen.getByTestId('verify-m3')
    expect(warn).toHaveTextContent('Passed with warnings')
    expect(warn).toHaveTextContent("counts don't reconcile")
    expect(warn).toHaveTextContent('2 keys collided')
    expect(screen.getByText('Verifying…')).toBeInTheDocument()

    await userEvent.click(within(bad).getByRole('button', { name: 'Mismatches (1)' }))
    const dialog = await screen.findByRole('dialog', { name: 'Mismatches in orders' })
    expect(dialog).toHaveTextContent('orders::3')
    expect(dialog).toHaveTextContent('Differs at $.total')
  })

  it('AC5: says so when verification is off', async () => {
    renderUI(<VerificationSection verifies={false} report={report([mappingReport('m1', 'customer')])} />)
    expect(await screen.findByText('Verification was turned off for this job.')).toBeInTheDocument()
    expect(screen.queryByTestId('verify-m1')).not.toBeInTheDocument()
  })
})
