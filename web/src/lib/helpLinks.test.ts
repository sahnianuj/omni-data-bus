import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { headingAnchor, helpLinks, helpUrl, type HelpTopic } from './helpLinks'

const guide = resolve(__dirname, '../../../docs/user-guide')

describe('help links (OC-057)', () => {
  it('AC4: every help link resolves', () => {
    for (const topic of Object.keys(helpLinks) as HelpTopic[]) {
      const { file, anchor } = helpLinks[topic]
      const path = resolve(guide, file)
      expect(existsSync(path), `${topic}: ${file}`).toBe(true)
      if (anchor) {
        const anchors = readFileSync(path, 'utf8')
          .split('\n')
          .filter((l) => /^#{1,6} /.test(l))
          .map((l) => headingAnchor(l.replace(/^#+ /, '')))
        expect(anchors, `${topic}: #${anchor}`).toContain(anchor)
      }
      expect(helpUrl(topic)).toMatch(/^https:\/\/github\.com\/.+\/docs\/user-guide\//)
    }
  })

  it('makes GitHub-style anchors', () => {
    expect(headingAnchor('Embedding a related table')).toBe('embedding-a-related-table')
    expect(headingAnchor('Running the script')).toBe('running-the-script')
  })

  it('OC-173 AC3: the design-first chapter is linked from the import and the guide', () => {
    expect(helpUrl('designFirst')).toMatch(/design-first\.md#importing-the-design$/)
    expect(readFileSync(resolve(guide, 'README.md'), 'utf8')).toContain('(design-first.md)')
  })
})

