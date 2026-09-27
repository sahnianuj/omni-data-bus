import type { components } from '@/lib/api/schema'

export type MappingView = components['schemas']['MappingView']
export type MappingSummary = components['schemas']['MappingSummary']
export type MappingSpec = components['schemas']['MappingSpec']
export type MappingTarget = components['schemas']['MappingTarget']
export type MappingStats = components['schemas']['MappingStats']
export type MappingIssue = components['schemas']['MappingIssue']
export type Explanation = components['schemas']['Explanation']
export type FieldMapping = components['schemas']['FieldMapping']
export type TransformStep = components['schemas']['TransformStep']
export type DocField = components['schemas']['DocField']
export type DocSchema = components['schemas']['DocSchema']
export type FieldType = components['schemas']['FieldType']
export type MappingPreview = components['schemas']['MappingPreview']
export type SourceColumn = components['schemas']['SourceColumn']
export type SourceTable = components['schemas']['SourceTable']
export type Embed = components['schemas']['Embed']
export type EmbedCandidate = components['schemas']['EmbedCandidate']
export type EmbedTable = components['schemas']['EmbedTable']
export type SourceRef = FieldMapping['sources'][number]
/** A canvas source row: a column of the table, or of an embedded table (name is then "embedId:column"). */
export type CanvasColumn = SourceColumn & { embed?: string; column?: string }
