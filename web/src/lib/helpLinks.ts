/** Where the user guide is read: the repository's docs/user-guide (OC-057). */
export const DOCS_BASE = 'https://github.com/sahnianuj/omni-data-bus/blob/main/docs/user-guide/'

/** The user-guide chapter behind each feature's help icon. */
export const helpLinks = {
  directoryTarget: { file: 'directory-targets.md', anchor: '' },
  embed: { file: 'relationship-mapping.md', anchor: 'embedding-a-related-table' },
  composer: { file: 'document-composer.md', anchor: '' },
  designFirst: { file: 'design-first.md', anchor: 'importing-the-design' },
  variants: { file: 'design-first.md', anchor: 'variant-shapes' },
  computed: { file: 'computed-fields.md', anchor: 'writing-a-template' },
  export: { file: 'running-outside-the-tool.md', anchor: 'exporting' },
} as const

export type HelpTopic = keyof typeof helpLinks

export function helpUrl(topic: HelpTopic): string {
  const { file, anchor } = helpLinks[topic]
  return DOCS_BASE + file + (anchor ? `#${anchor}` : '')
}

/** GitHub's anchor for a Markdown heading (lower-case, punctuation dropped, spaces to dashes). */
export function headingAnchor(heading: string): string {
  return heading
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .replace(/\s/g, '-')
}
