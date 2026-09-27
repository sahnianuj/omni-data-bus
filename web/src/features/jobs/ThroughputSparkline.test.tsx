import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { pushPoint, SPARK_POINTS, ThroughputSparkline } from './ThroughputSparkline'

describe('ThroughputSparkline', () => {
  it('AC3: ring buffer caps at 60', () => {
    let pts: number[] = []
    for (let i = 1; i <= 75; i++) pts = pushPoint(pts, i)
    expect(pts).toHaveLength(SPARK_POINTS)
    expect(pts[0]).toBe(16)
    expect(pts[59]).toBe(75)
    render(<ThroughputSparkline points={[100, 20_000, 21_000]} />)
    expect(screen.getByRole('img')).toHaveAccessibleName('Throughput over the last 3 seconds, from 100 to 21.0k docs/s')
  })
})
