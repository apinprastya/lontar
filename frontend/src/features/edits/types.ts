/**
 * Frontend view of the Go edit models (internal/edits, internal/services).
 * The UI never builds SQL for edits: it sends a structured change set and
 * Go generates parameterised statements.
 */
import type { Cell } from '../workspace/types'

/** A new cell value: NULL, the column default, typed text or hex bytes. */
export type EditValue = { kind: 'null' } | { kind: 'default' } | { kind: 'value'; text: string } | { kind: 'bytes'; hex: string }

export interface Update {
  pk: Record<string, Cell>
  set: Record<string, EditValue>
  old: Record<string, Cell>
}

export interface Insert {
  values: Record<string, EditValue>
}

export interface Delete {
  pk: Record<string, Cell>
}

export interface ChangeSet {
  schema: string
  table: string
  updates: Update[]
  inserts: Insert[]
  deletes: Delete[]
}

export type Op = 'update' | 'insert' | 'delete'

export interface PreviewStatement {
  op: Op
  index: number
  /** Display SQL with literals; never executed. */
  sql: string
}

export type FailureKind = 'invalid' | 'readonly' | 'stale' | 'duplicate' | 'database'

export interface ChangeFailure {
  op?: Op | ''
  /** Position in updates/inserts/deletes; -1 for the whole set. */
  index: number
  kind: FailureKind
  column?: string
  message: string
  sql?: string
}

export interface ApplyResult {
  ok: boolean
  result: { deleted: number; updated: number; inserted: number; insertedKeys: (Record<string, Cell> | null)[] } | null
  failure: ChangeFailure | null
}

export type SafeMode = 'off' | 'confirm' | 'readonly'

export const SAFE_MODES: { value: SafeMode; label: string; hint: string }[] = [
  { value: 'off', label: 'Off', hint: 'No restrictions' },
  { value: 'confirm', label: 'Confirm writes', hint: 'Ask before running writes and before committing edits' },
  { value: 'readonly', label: 'Read-only', hint: 'The database rejects every write' },
]

export function safeModeLabel(m: SafeMode | undefined): string {
  return SAFE_MODES.find((s) => s.value === m)?.label ?? 'Off'
}
