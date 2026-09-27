import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { ExpressionEditor, offsetOf } from './ExpressionEditor'
import { TransformPipelineEditor } from './TransformPipelineEditor'
import type { MappingIssue } from './types'

const issue = (line: number, column: number, message: string): MappingIssue => ({
  severity: 'error', code: 'TRANSFORM_EXPRESSION_INVALID', path: 'total', message, fieldMappingId: 'm1', opIndex: 0, line, column,
})

describe('ExpressionEditor', () => {
  it('AC8: shows server diagnostics', async () => {
    const expr = 'upper(value)\n  + row.cuntry'
    render(<ExpressionEditor id="e" value={expr} onChange={() => {}} issues={[issue(2, 5, "unknown field 'cuntry'")]} />)

    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent("Line 2, column 5: unknown field 'cuntry'")
    expect(alert).toHaveTextContent('+ row.cuntry')
    expect(screen.getByTestId('expr-caret').textContent).toBe('    ^')
    const box = screen.getByRole('textbox')
    expect(box).toHaveAttribute('aria-invalid', 'true')

    // Clicking the diagnostic puts the cursor at line 2, column 5.
    await userEvent.click(alert)
    expect(box).toHaveFocus()
    expect((box as HTMLTextAreaElement).selectionStart).toBe(expr.indexOf('row'))
  })

  it('shows diagnostics for the expression step inside the pipeline', () => {
    render(<TransformPipelineEditor steps={[{ op: 'expression', args: { expr: 'value +' } }]} onChange={() => {}} issues={[issue(1, 8, 'unexpected end of input')]} />)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent('Line 1, column 8: unexpected end of input')
    expect(screen.getByRole('textbox', { name: 'Expression' })).toHaveValue('value +')
  })

  it('offsetOf maps 1-based positions and clamps', () => {
    expect(offsetOf('ab\ncd', 1, 1)).toBe(0)
    expect(offsetOf('ab\ncd', 2, 2)).toBe(4)
    expect(offsetOf('ab\ncd', 2, 99)).toBe(5)
    expect(offsetOf('ab', 7, 1)).toBe(0)
  })
})
