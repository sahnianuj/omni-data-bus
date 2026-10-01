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
export type Relation = components['schemas']['Relation']
/** The canvas still calls relations embeds (OC-021); spec v2 names them relations (OC-037). */
export type Embed = Relation
export type EmbedCandidate = components['schemas']['EmbedCandidate']
export type EmbedTable = components['schemas']['EmbedTable']
export type SourceRef = FieldMapping['sources'][number]
/**
 * A canvas source row: a column of the table, or of a relation's table (name is then
 * "relationId:column"), or a many-to-many junction column (from 'via', "relationId:via:column").
 */
export type CanvasColumn = SourceColumn & { embed?: string; column?: string; from?: 'via' }
export type Aggregate = components['schemas']['Aggregate']
export type RelationGraph = components['schemas']['RelationGraph']
export type RelationHop = components['schemas']['RelationHop']
export type RelationPath = components['schemas']['RelationPath']
export type JunctionCandidate = components['schemas']['JunctionCandidate']
export type IndexAdvice = components['schemas']['IndexAdvice']
export type Relationship = components['schemas']['Relationship']
export type RelationshipInput = components['schemas']['RelationshipInput']
export type RelationshipDetection = components['schemas']['RelationshipDetection']
export type RelationFilter = components['schemas']['RelationFilter']
