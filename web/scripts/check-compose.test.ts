import { readFileSync } from 'node:fs'
import path from 'node:path'
import { parse } from 'yaml'
import { describe, expect, it } from 'vitest'

type Service = { healthcheck?: unknown; restart?: string; image?: string }
const compose = parse(
  readFileSync(path.resolve(__dirname, '../../deploy/compose/dev.yml'), 'utf8'),
) as { services: Record<string, Service> }

describe('dev compose', () => {
  it('AC1: all long-running services declare healthchecks', () => {
    const longRunning = Object.entries(compose.services).filter(([, s]) => s.restart !== 'no')
    expect(longRunning.length).toBeGreaterThanOrEqual(2)
    for (const [name, svc] of longRunning) {
      expect(svc.healthcheck, `${name} needs a healthcheck`).toBeDefined()
    }
  })

  it('AC1: uses Couchbase Server 8.0.3 for the metadata store', () => {
    expect(compose.services.couchbase?.image).toBe('couchbase/server:enterprise-8.0.3')
  })
})
