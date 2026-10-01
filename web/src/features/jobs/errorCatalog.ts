import type { RowErrorCode } from './api'

type Entry = { title: string; hint: string }

/** A friendly description and fix hint for every row error code (OC-026). */
export const errorCatalog: Record<RowErrorCode, Entry> = {
  PARSE_FAILED: { title: "Value couldn't be converted", hint: 'Consider a non-strict parse, or a default value for bad input.' },
  TYPE_MISMATCH: { title: "Value doesn't match the field type", hint: 'Add a conversion step, or change the field type in the design.' },
  EXPR_ERROR: { title: 'Expression failed', hint: 'Check the expression against this row, e.g. guard nulls with coalesce().' },
  NON_FINITE_NUMBER: { title: 'Number is NaN or infinite', hint: "JSON can't store it: map it to null with a nullIf step, or use a default." },
  TRANSFORM_INVALID: { title: 'Transform is misconfigured', hint: 'Open the mapping and fix the step flagged by Validate.' },
  KEY_TEMPLATE_INVALID: { title: "Document key couldn't be built", hint: 'Fix the key template in the mapping.' },
  KEY_NULL_COMPONENT: { title: 'Key column is empty', hint: 'Use columns that are never null in the key template, or add a default.' },
  KEY_TOO_LONG: { title: 'Document key is longer than 250 bytes', hint: 'Shorten the key template, e.g. with a hash filter.' },
  KEY_INVALID: { title: 'Document key was rejected', hint: 'Keys must be 1–250 bytes of valid UTF-8.' },
  DOC_TOO_LARGE: { title: "Document is over the target's size limit", hint: 'Split large columns out, or omit them from the design (Couchbase allows 20 MB; a directory its largest document setting).' },
  TEMPORARY_FAILURE_EXHAUSTED: { title: 'Couchbase stayed busy', hint: 'Lower the write concurrency or add a throttle, then retry the failed rows.' },
  WRITE_FAILED: { title: "The target didn't accept the document", hint: 'See the message for details, then retry the failed rows.' },
  KEY_PATH_COLLISION: { title: 'Two keys map to the same file', hint: "The target folder ignores letter case: make the key template's text columns lower-case, or choose a case-sensitive folder." },
  DECODE_FAILED: { title: "Source value couldn't be read", hint: 'The column type may be unsupported; cast it in a view or exclude it.' },
}

export function describeError(code: string): Entry {
  return (errorCatalog as Record<string, Entry>)[code] ?? { title: code, hint: '' }
}
