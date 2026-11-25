/**
 * Staged structure edits of one table, as immutable data with pure update
 * functions. The draft is the desired structure; toTableChange diffs it
 * against the structure it was made from and produces the ddl.TableChange
 * Go validates and applies (the UI never builds DDL).
 *
 * Rows are identified by keys that survive renames: existing columns are
 * "c:<original name>", new rows "n:<id>". The primary key, index parts and
 * foreign key columns refer to columns by key, so renaming a column in the
 * same change keeps them pointing at it.
 */
import type { CheckInfo, ColumnDetail, ForeignKey, IndexInfo, TableDetail, TriggerInfo } from '../features/structure/types'
import type { Caps, ChangeFailure, ColumnChange, DefaultKind, IndexColumn, Section, TableChange } from '../features/structure/editTypes'

/** How a column gets its value: a default kind, or generated (new columns). */
export type DefaultMode = DefaultKind | 'generatedStored' | 'generatedVirtual'

export interface ColumnDraft {
  key: string
  original: ColumnDetail | null
  name: string
  /** The type as written; "enum"/"set" (MySQL) keep their values in enumValues. */
  type: string
  enumValues: string[]
  nullable: boolean
  defaultMode: DefaultMode
  /** The default value or expression (or the generation expression). */
  defaultText: string
  comment: string
  collation: string
  autoIncrement: boolean
  /** PostgreSQL USING expression for a type change. */
  using: string
  dropped: boolean
}

export interface IndexPartDraft {
  /** Column key, or "" for an expression. */
  column: string
  expr: string
  desc: boolean
  length: number
}

export interface IndexDraft {
  key: string
  original: IndexInfo | null
  name: string
  parts: IndexPartDraft[]
  unique: boolean
  method: string
  predicate: string
  dropped: boolean
}

export interface FKDraft {
  key: string
  original: ForeignKey | null
  name: string
  /** Column keys of this table. */
  columns: string[]
  refSchema: string
  refTable: string
  refColumns: string[]
  onUpdate: string
  onDelete: string
  dropped: boolean
}

export interface CheckDraft {
  key: string
  original: CheckInfo | null
  name: string
  expression: string
  dropped: boolean
}

export interface TriggerDraft {
  original: TriggerInfo
  dropped: boolean
}

export interface StructureDraft {
  name: string
  comment: string
  columns: ColumnDraft[]
  /** Primary key column keys in key order. */
  pk: string[]
  indexes: IndexDraft[]
  fks: FKDraft[]
  checks: CheckDraft[]
  triggers: TriggerDraft[]
  nextId: number
}

/** Draft keys per section, by position in the TableChange (to place failures). */
export type Origins = Record<'columns' | 'indexes' | 'foreignKeys' | 'checks' | 'triggers', string[]>

export const ENUM_RE = /^(enum|set)$/i
const ENUM_TYPE_RE = /^(enum|set)\(/i

/** The draft type of a column: MySQL enum(...)/set(...) split into a keyword and values. */
export function splitType(c: Pick<ColumnDetail, 'dataType' | 'enumValues'>, caps: Caps | null): { type: string; values: string[] } {
  const m = caps?.enumTypes ? ENUM_TYPE_RE.exec(c.dataType) : null
  if (m) return { type: m[1].toLowerCase(), values: c.enumValues ?? [] }
  return { type: c.dataType, values: [] }
}

/** How an existing column gets its value, as a draft default. */
export function originalDefault(c: ColumnDetail): { mode: DefaultMode; text: string } {
  if (c.generated) return { mode: c.generated === 'VIRTUAL' ? 'generatedVirtual' : 'generatedStored', text: c.generationExpr ?? '' }
  if (c.default === null) return { mode: 'none', text: '' }
  return { mode: 'expression', text: c.default }
}

function columnDraft(c: ColumnDetail, caps: Caps | null): ColumnDraft {
  const t = splitType(c, caps)
  const d = originalDefault(c)
  return {
    key: `c:${c.name}`,
    original: c,
    name: c.name,
    type: t.type,
    enumValues: t.values,
    nullable: c.nullable,
    defaultMode: d.mode,
    defaultText: d.text,
    comment: c.comment,
    collation: c.collation,
    autoIncrement: c.autoIncrement,
    using: '',
    dropped: false,
  }
}

/** The draft of an existing table (nothing changed yet). */
export function draftFromDetail(d: TableDetail, caps: Caps | null): StructureDraft {
  const columns = d.columns.map((c) => columnDraft(c, caps))
  const keyOf = (name: string) => columns.find((c) => c.name === name)?.key ?? `c:${name}`
  const pk = [...d.columns].filter((c) => c.isPrimaryKey).sort((a, b) => a.pkPosition - b.pkPosition).map((c) => keyOf(c.name))
  return {
    name: d.name,
    comment: d.comment,
    columns,
    pk,
    indexes: d.indexes
      .filter((i) => !i.primary)
      .map((i) => ({ key: `i:${i.name}`, original: i, name: i.name, parts: [], unique: i.unique, method: i.method, predicate: i.predicate, dropped: false })),
    fks: d.foreignKeys.map((f) => ({
      key: `f:${f.name}`,
      original: f,
      name: f.name,
      columns: f.columns.map(keyOf),
      refSchema: f.refSchema,
      refTable: f.refTable,
      refColumns: f.refColumns,
      onUpdate: f.onUpdate,
      onDelete: f.onDelete,
      dropped: false,
    })),
    checks: d.checks.map((c) => ({ key: `k:${c.name}`, original: c, name: c.name, expression: c.expression, dropped: false })),
    triggers: d.triggers.map((t) => ({ original: t, dropped: false })),
    nextId: 1,
  }
}

/** The usual integer type of an engine (new tables, new id columns). */
export function intType(caps: Caps | null): string {
  switch (caps?.flavor) {
    case 'sqlite':
      return 'INTEGER'
    case 'mysql':
    case 'mariadb':
      return 'int'
  }
  return 'integer'
}

/** A new column (name "" until the user types one). */
export function newColumn(key: string, over: Partial<ColumnDraft> = {}): ColumnDraft {
  return {
    key,
    original: null,
    name: '',
    type: '',
    enumValues: [],
    nullable: true,
    defaultMode: 'none',
    defaultText: '',
    comment: '',
    collation: '',
    autoIncrement: false,
    using: '',
    dropped: false,
    ...over,
  }
}

/** The draft of a new table: an auto-increment integer key "id". */
export function newTableDraft(caps: Caps | null): StructureDraft {
  const id = newColumn('n:1', { name: 'id', type: intType(caps), nullable: false, autoIncrement: true })
  return { name: '', comment: '', columns: [id], pk: [id.key], indexes: [], fks: [], checks: [], triggers: [], nextId: 2 }
}

// ---- Updates --------------------------------------------------------------

/** The final name of a column key ("" when unknown). */
export function columnName(d: StructureDraft, key: string): string {
  return d.columns.find((c) => c.key === key)?.name ?? ''
}

const live = <T extends { dropped: boolean }>(xs: T[]) => xs.filter((x) => !x.dropped)

export function addColumn(d: StructureDraft, over: Partial<ColumnDraft> = {}): StructureDraft {
  return { ...d, columns: [...d.columns, newColumn(`n:${d.nextId}`, over)], nextId: d.nextId + 1 }
}

export function updateColumn(d: StructureDraft, key: string, patch: Partial<ColumnDraft>): StructureDraft {
  return { ...d, columns: d.columns.map((c) => (c.key === key ? { ...c, ...patch } : c)) }
}

/** Drops (or restores) a column; a new column is removed, with its references. */
export function toggleDropColumn(d: StructureDraft, key: string): StructureDraft {
  const c = d.columns.find((x) => x.key === key)
  if (!c) return d
  if (!c.original) {
    return {
      ...d,
      columns: d.columns.filter((x) => x.key !== key),
      pk: d.pk.filter((k) => k !== key),
      indexes: d.indexes.filter((i) => i.original || !i.parts.some((p) => p.column === key)),
      fks: d.fks.filter((f) => f.original || !f.columns.includes(key)),
    }
  }
  return updateColumn(d, key, { dropped: !c.dropped })
}

/** Adds a column to the primary key (at the end) or removes it. */
export function togglePK(d: StructureDraft, key: string): StructureDraft {
  return { ...d, pk: d.pk.includes(key) ? d.pk.filter((k) => k !== key) : [...d.pk, key] }
}

export function addIndex(d: StructureDraft, x: Omit<IndexDraft, 'key' | 'original' | 'dropped'>): StructureDraft {
  return { ...d, indexes: [...d.indexes, { ...x, key: `n:${d.nextId}`, original: null, dropped: false }], nextId: d.nextId + 1 }
}

export function renameIndex(d: StructureDraft, key: string, name: string): StructureDraft {
  return { ...d, indexes: d.indexes.map((i) => (i.key === key ? { ...i, name } : i)) }
}

function toggleDrop<T extends { key: string; original: unknown; dropped: boolean }>(xs: T[], key: string): T[] {
  const x = xs.find((i) => i.key === key)
  if (!x) return xs
  if (!x.original) return xs.filter((i) => i.key !== key)
  return xs.map((i) => (i.key === key ? { ...i, dropped: !i.dropped } : i))
}

export function toggleDropIndex(d: StructureDraft, key: string): StructureDraft {
  return { ...d, indexes: toggleDrop(d.indexes, key) }
}

export function addFK(d: StructureDraft, x: Omit<FKDraft, 'key' | 'original' | 'dropped'>): StructureDraft {
  return { ...d, fks: [...d.fks, { ...x, key: `n:${d.nextId}`, original: null, dropped: false }], nextId: d.nextId + 1 }
}

export function toggleDropFK(d: StructureDraft, key: string): StructureDraft {
  return { ...d, fks: toggleDrop(d.fks, key) }
}

export function addCheck(d: StructureDraft, x: { name: string; expression: string }): StructureDraft {
  return { ...d, checks: [...d.checks, { ...x, key: `n:${d.nextId}`, original: null, dropped: false }], nextId: d.nextId + 1 }
}

export function updateCheck(d: StructureDraft, key: string, patch: Partial<Pick<CheckDraft, 'name' | 'expression'>>): StructureDraft {
  return { ...d, checks: d.checks.map((c) => (c.key === key && !c.original ? { ...c, ...patch } : c)) }
}

export function toggleDropCheck(d: StructureDraft, key: string): StructureDraft {
  return { ...d, checks: toggleDrop(d.checks, key) }
}

export function toggleDropTrigger(d: StructureDraft, name: string): StructureDraft {
  return { ...d, triggers: d.triggers.map((t) => (t.original.name === name ? { ...t, dropped: !t.dropped } : t)) }
}

// ---- Diff -----------------------------------------------------------------

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i])

function emptyColumnChange(op: ColumnChange['op'], name: string): ColumnChange {
  return { op, name, newName: '', type: null, nullable: null, default: null, comment: null, collation: null, autoIncrement: null, using: null, generated: null }
}

const typeSpec = (c: ColumnDraft) => ({ text: c.type.trim(), enumValues: ENUM_RE.test(c.type.trim()) ? c.enumValues : null })

/** The fields of an existing column that changed (empty: none). */
export function changedFields(c: ColumnDraft, caps: Caps | null): string[] {
  const o = c.original
  if (!o) return []
  const out: string[] = []
  const t = splitType(o, caps)
  if (c.name !== o.name) out.push('name')
  if (c.type.trim() !== t.type || (ENUM_RE.test(c.type.trim()) && !sameList(c.enumValues, t.values))) out.push('type')
  if (c.nullable !== o.nullable) out.push('nullable')
  const d = originalDefault(o)
  if (c.defaultMode !== d.mode || c.defaultText !== d.text) out.push('default')
  if (c.comment !== o.comment) out.push('comment')
  if (c.collation !== o.collation) out.push('collation')
  if (c.autoIncrement !== o.autoIncrement) out.push('autoIncrement')
  return out
}

function alterColumn(c: ColumnDraft, caps: Caps | null): ColumnChange | null {
  const fields = changedFields(c, caps)
  if (fields.length === 0) return null
  const ch = emptyColumnChange('alter', c.original!.name)
  for (const f of fields) {
    switch (f) {
      case 'name':
        ch.newName = c.name
        break
      case 'type':
        ch.type = typeSpec(c)
        if (c.using.trim()) ch.using = c.using
        break
      case 'nullable':
        ch.nullable = c.nullable
        break
      case 'default':
        if (c.defaultMode !== 'generatedStored' && c.defaultMode !== 'generatedVirtual') ch.default = { kind: c.defaultMode, text: c.defaultText }
        break
      case 'comment':
        ch.comment = c.comment
        break
      case 'collation':
        ch.collation = c.collation
        break
      case 'autoIncrement':
        ch.autoIncrement = c.autoIncrement
        break
    }
  }
  return ch
}

function addColumnChange(c: ColumnDraft): ColumnChange {
  const ch = emptyColumnChange('add', c.name)
  ch.type = typeSpec(c)
  ch.nullable = c.nullable
  if (c.defaultMode === 'generatedStored' || c.defaultMode === 'generatedVirtual') {
    ch.generated = { expr: c.defaultText, stored: c.defaultMode === 'generatedStored' }
  } else if (c.defaultMode !== 'none') {
    ch.default = { kind: c.defaultMode, text: c.defaultText }
  }
  if (c.comment) ch.comment = c.comment
  if (c.collation) ch.collation = c.collation
  if (c.autoIncrement) ch.autoIncrement = true
  return ch
}

/**
 * The change set of a draft: base is the structure it was made from (null:
 * a new table in schema). origins maps each entry back to its draft key.
 */
export function toTableChange(schema: string, base: TableDetail | null, d: StructureDraft, caps: Caps | null): { change: TableChange; origins: Origins } {
  const create = base === null
  const origins: Origins = { columns: [], indexes: [], foreignKeys: [], checks: [], triggers: [] }
  const change: TableChange = {
    schema,
    table: create ? d.name.trim() : base.name,
    create,
    base,
    newName: null,
    comment: null,
    columns: [],
    primaryKey: null,
    indexes: [],
    foreignKeys: [],
    checks: [],
    dropTriggers: [],
  }
  if (create) {
    if (d.comment) change.comment = d.comment
  } else {
    if (d.name !== base.name) change.newName = d.name
    if (d.comment !== base.comment) change.comment = d.comment
  }
  for (const c of d.columns) {
    let ch: ColumnChange | null = null
    if (!c.original) ch = c.dropped ? null : addColumnChange(c)
    else if (c.dropped) ch = emptyColumnChange('drop', c.original.name)
    else ch = alterColumn(c, caps)
    if (ch) {
      change.columns.push(ch)
      origins.columns.push(c.key)
    }
  }
  const pkNames = d.pk.filter((k) => d.columns.some((c) => c.key === k && !c.dropped)).map((k) => columnName(d, k))
  if (create) {
    if (pkNames.length > 0) change.primaryKey = pkNames
  } else {
    const basePK = draftFromDetail(base, caps).pk
    const draftPK = d.pk.filter((k) => d.columns.some((c) => c.key === k && !c.dropped))
    if (!sameList(basePK, draftPK)) change.primaryKey = pkNames
  }
  for (const x of d.indexes) {
    if (x.original) {
      if (x.dropped) change.indexes.push({ op: 'drop', name: x.original.name, newName: '', columns: [], unique: false, method: '', predicate: '' })
      else if (x.name !== x.original.name) change.indexes.push({ op: 'rename', name: x.original.name, newName: x.name, columns: [], unique: false, method: '', predicate: '' })
      else continue
    } else {
      const columns: IndexColumn[] = x.parts.map((p) => ({ column: p.column ? columnName(d, p.column) : '', expr: p.column ? '' : p.expr, desc: p.desc, length: p.length }))
      change.indexes.push({ op: 'add', name: x.name, newName: '', columns, unique: x.unique, method: x.method, predicate: x.predicate })
    }
    origins.indexes.push(x.key)
  }
  for (const f of d.fks) {
    if (f.original) {
      if (!f.dropped) continue
      change.foreignKeys.push({ op: 'drop', name: f.original.name, columns: [], refSchema: '', refTable: '', refColumns: [], onUpdate: '', onDelete: '' })
    } else {
      change.foreignKeys.push({ op: 'add', name: f.name, columns: f.columns.map((k) => columnName(d, k)), refSchema: f.refSchema, refTable: f.refTable, refColumns: f.refColumns, onUpdate: f.onUpdate, onDelete: f.onDelete })
    }
    origins.foreignKeys.push(f.key)
  }
  for (const c of d.checks) {
    if (c.original) {
      if (!c.dropped) continue
      change.checks.push({ op: 'drop', name: c.original.name, expression: '' })
    } else {
      change.checks.push({ op: 'add', name: c.name, expression: c.expression })
    }
    origins.checks.push(c.key)
  }
  for (const t of d.triggers) {
    if (t.dropped) {
      change.dropTriggers.push(t.original.name)
      origins.triggers.push(t.original.name)
    }
  }
  return { change, origins }
}

/** Whether a change set has anything to apply. */
export function hasChanges(tc: TableChange): boolean {
  return (
    tc.create ||
    tc.newName !== null ||
    tc.comment !== null ||
    tc.columns.length > 0 ||
    tc.primaryKey !== null ||
    tc.indexes.length > 0 ||
    tc.foreignKeys.length > 0 ||
    tc.checks.length > 0 ||
    tc.dropTriggers.length > 0
  )
}

/** The draft key a failure is about: a row key, "table" or "pk" (null: none). */
export function failureKey(f: ChangeFailure, origins: Origins): string | null {
  const sections: Partial<Record<Section, keyof Origins>> = { columns: 'columns', indexes: 'indexes', foreignKeys: 'foreignKeys', checks: 'checks', triggers: 'triggers' }
  if (f.section === 'table') return 'table'
  if (f.section === 'primaryKey') return 'pk'
  const s = sections[f.section]
  return (s && origins[s][f.index]) ?? null
}

/** Row state for styling. */
export type RowState = 'new' | 'dropped' | 'modified' | 'same'

export function columnState(c: ColumnDraft, caps: Caps | null): RowState {
  if (!c.original) return 'new'
  if (c.dropped) return 'dropped'
  return changedFields(c, caps).length > 0 ? 'modified' : 'same'
}

export function itemState(x: { original: unknown; dropped: boolean; name?: string }, originalName?: string): RowState {
  if (!x.original) return 'new'
  if (x.dropped) return 'dropped'
  return originalName !== undefined && x.name !== originalName ? 'modified' : 'same'
}

/** Columns that are not dropped (pickers). */
export function liveColumns(d: StructureDraft): ColumnDraft[] {
  return live(d.columns)
}
