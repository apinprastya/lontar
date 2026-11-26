import { describe, expect, it } from 'vitest'
import { editableDetail, makeCaps, makeColumn } from '../test/structure'
import {
  addCheck,
  addColumn,
  addFK,
  addIndex,
  changedFields,
  columnName,
  columnState,
  draftFromDetail,
  failureKey,
  hasChanges,
  intType,
  itemState,
  liveColumns,
  newTableDraft,
  originalDefault,
  renameIndex,
  splitType,
  toTableChange,
  toggleDropCheck,
  toggleDropColumn,
  toggleDropFK,
  toggleDropIndex,
  toggleDropTrigger,
  togglePK,
  updateCheck,
  updateColumn,
} from './structureEdits'

const caps = makeCaps()

describe('structure drafts', () => {
  it('starts from the table with nothing to change', () => {
    const base = editableDetail()
    const d = draftFromDetail(base, caps)
    expect(d.columns.map((c) => c.key)).toEqual(['c:id', 'c:email', 'c:org_id', 'c:total'])
    expect(d.pk).toEqual(['c:id'])
    expect(d.indexes.map((i) => i.name)).toEqual(['users_email_idx'])
    expect(d.fks[0].columns).toEqual(['c:org_id'])
    expect(d.columns[3]).toMatchObject({ defaultMode: 'generatedStored', defaultText: 'id * 2' })
    const { change } = toTableChange('public', base, d, caps)
    expect(hasChanges(change)).toBe(false)
    expect(change).toMatchObject({ schema: 'public', table: 'users', create: false, base })
  })

  it('diffs columns: add, alter (only what changed), drop', () => {
    const base = editableDetail()
    let d = draftFromDetail(base, caps)
    d = updateColumn(d, 'c:email', { name: 'mail', type: 'varchar(200)', using: 'email::varchar', nullable: true, defaultMode: 'value', defaultText: 'x', comment: '' })
    d = toggleDropColumn(d, 'c:org_id')
    d = addColumn(d, { name: 'created', type: 'timestamptz', defaultMode: 'expression', defaultText: 'now()', comment: 'when', nullable: false })
    d = addColumn(d, { name: 'g', type: 'int', defaultMode: 'generatedStored', defaultText: 'id + 1', collation: 'C', autoIncrement: true })
    d = updateColumn(d, 'c:id', { autoIncrement: false, collation: 'x' })
    expect(columnState(d.columns[1], caps)).toBe('modified')
    expect(columnState(d.columns[2], caps)).toBe('dropped')
    expect(columnState(d.columns[4], caps)).toBe('new')
    expect(columnState(d.columns[3], caps)).toBe('same')
    expect(changedFields(d.columns[4], caps)).toEqual([])
    const { change, origins } = toTableChange('public', base, d, caps)
    expect(change.columns).toEqual([
      { op: 'alter', name: 'id', newName: '', type: null, nullable: null, default: null, comment: null, collation: 'x', autoIncrement: false, using: null, generated: null },
      { op: 'alter', name: 'email', newName: 'mail', type: { text: 'varchar(200)', enumValues: null }, nullable: true, default: { kind: 'value', text: 'x' }, comment: '', collation: null, autoIncrement: null, using: 'email::varchar', generated: null },
      { op: 'drop', name: 'org_id', newName: '', type: null, nullable: null, default: null, comment: null, collation: null, autoIncrement: null, using: null, generated: null },
      { op: 'add', name: 'created', newName: '', type: { text: 'timestamptz', enumValues: null }, nullable: false, default: { kind: 'expression', text: 'now()' }, comment: 'when', collation: null, autoIncrement: null, using: null, generated: null },
      { op: 'add', name: 'g', newName: '', type: { text: 'int', enumValues: null }, nullable: true, default: null, comment: null, collation: 'C', autoIncrement: true, using: null, generated: { expr: 'id + 1', stored: true } },
    ])
    expect(origins.columns).toEqual(['c:id', 'c:email', 'c:org_id', 'n:1', 'n:2'])
    // Restoring a dropped column undoes it; removing a new one forgets it.
    d = toggleDropColumn(toggleDropColumn(d, 'c:org_id'), 'n:2')
    expect(d.columns.find((c) => c.key === 'c:org_id')?.dropped).toBe(false)
    expect(d.columns.some((c) => c.key === 'n:2')).toBe(false)
    expect(toggleDropColumn(d, 'nope')).toBe(d)
    // A generated default is never sent as a default for an existing column.
    const g = updateColumn(draftFromDetail(base, caps), 'c:total', { defaultMode: 'generatedVirtual' })
    expect(toTableChange('public', base, g, caps).change.columns[0].default).toBeNull()
  })

  it('handles MySQL enum types as a keyword and values', () => {
    const my = makeCaps({ flavor: 'mysql', enumTypes: true })
    const col = makeColumn({ name: 'k', dataType: "enum('a','b')", enumValues: ['a', 'b'] })
    expect(splitType(col, my)).toEqual({ type: 'enum', values: ['a', 'b'] })
    expect(splitType(col, caps)).toEqual({ type: "enum('a','b')", values: [] })
    expect(splitType({ ...col, enumValues: null }, my).values).toEqual([])
    const base = { ...editableDetail(), columns: [col] }
    let d = draftFromDetail(base, my)
    expect(hasChanges(toTableChange('s', base, d, my).change)).toBe(false)
    d = updateColumn(d, 'c:k', { enumValues: ['a', 'b', 'c'] })
    expect(toTableChange('s', base, d, my).change.columns[0].type).toEqual({ text: 'enum', enumValues: ['a', 'b', 'c'] })
  })

  it('reads existing defaults', () => {
    expect(originalDefault(makeColumn({ default: null }))).toEqual({ mode: 'none', text: '' })
    expect(originalDefault(makeColumn({ default: 'now()' }))).toEqual({ mode: 'expression', text: 'now()' })
    expect(originalDefault(makeColumn({ generated: 'VIRTUAL', generationExpr: null }))).toEqual({ mode: 'generatedVirtual', text: '' })
  })

  it('diffs the key, indexes, foreign keys, checks, triggers and the table', () => {
    const base = editableDetail()
    let d = draftFromDetail(base, caps)
    d = updateColumn(d, 'c:email', { name: 'mail' })
    d = togglePK(d, 'c:email')
    d = { ...d, name: 'people', comment: '' }
    d = renameIndex(d, 'i:users_email_idx', 'mail_idx')
    d = addIndex(d, { name: 'by_mail', parts: [{ column: 'c:email', expr: '', desc: true, length: 0 }, { column: '', expr: 'lower(mail)', desc: false, length: 0 }], unique: true, method: 'btree', predicate: 'id > 0' })
    d = toggleDropFK(d, 'f:users_org_fk')
    d = addFK(d, { name: 'fk2', columns: ['c:org_id'], refSchema: 'public', refTable: 'orgs', refColumns: ['id'], onUpdate: '', onDelete: 'CASCADE' })
    d = toggleDropCheck(d, 'k:users_email_check')
    d = addCheck(d, { name: '', expression: 'id > 0' })
    d = updateCheck(d, 'n:3', { name: 'c2' })
    d = updateCheck(d, 'k:users_email_check', { name: 'ignored' })
    d = toggleDropTrigger(d, 'users_touch')
    expect(itemState(d.indexes[0], 'users_email_idx')).toBe('modified')
    expect(itemState(d.fks[0])).toBe('dropped')
    expect(itemState(d.checks[1])).toBe('new')
    expect(itemState({ original: {}, dropped: false })).toBe('same')
    const { change, origins } = toTableChange('public', base, d, caps)
    expect(change.newName).toBe('people')
    expect(change.comment).toBe('')
    expect(change.primaryKey).toEqual(['id', 'mail'])
    expect(change.indexes).toEqual([
      { op: 'rename', name: 'users_email_idx', newName: 'mail_idx', columns: [], unique: false, method: '', predicate: '' },
      { op: 'add', name: 'by_mail', newName: '', columns: [{ column: 'mail', expr: '', desc: true, length: 0 }, { column: '', expr: 'lower(mail)', desc: false, length: 0 }], unique: true, method: 'btree', predicate: 'id > 0' },
    ])
    expect(change.foreignKeys.map((f) => [f.op, f.name, f.columns])).toEqual([['drop', 'users_org_fk', []], ['add', 'fk2', ['org_id']]])
    expect(change.checks).toEqual([{ op: 'drop', name: 'users_email_check', expression: '' }, { op: 'add', name: 'c2', expression: 'id > 0' }])
    expect(change.dropTriggers).toEqual(['users_touch'])
    expect(origins).toEqual({ columns: ['c:email'], indexes: ['i:users_email_idx', 'n:1'], foreignKeys: ['f:users_org_fk', 'n:2'], checks: ['k:users_email_check', 'n:3'], triggers: ['users_touch'] })

    // Dropping an index and removing new rows.
    let e = toggleDropIndex(d, 'i:users_email_idx')
    expect(toTableChange('public', base, e, caps).change.indexes[0].op).toBe('drop')
    e = toggleDropIndex(toggleDropFK(toggleDropCheck(e, 'n:3'), 'n:2'), 'n:1')
    expect(e.indexes).toHaveLength(1)
    expect(e.fks).toHaveLength(1)
    expect(e.checks).toHaveLength(1)
    expect(toggleDropIndex(e, 'nope')).toEqual(e)
    // Removing a new column removes the new index and FK that use it.
    let f = addColumn(draftFromDetail(base, caps), { name: 'x', type: 'int' })
    f = togglePK(f, 'n:1')
    f = addIndex(f, { name: 'xi', parts: [{ column: 'n:1', expr: '', desc: false, length: 0 }], unique: false, method: '', predicate: '' })
    f = addFK(f, { name: '', columns: ['n:1'], refSchema: '', refTable: 'orgs', refColumns: ['id'], onUpdate: '', onDelete: '' })
    f = toggleDropColumn(f, 'n:1')
    expect([f.pk, f.indexes.length, f.fks.length]).toEqual([['c:id'], 1, 1])
    // Dropping a key column changes the key; untoggling it does too.
    const g = togglePK(draftFromDetail(base, caps), 'c:id')
    expect(toTableChange('public', base, g, caps).change.primaryKey).toEqual([])
    expect(toTableChange('public', base, toggleDropColumn(draftFromDetail(base, caps), 'c:id'), caps).change.primaryKey).toEqual([])
  })

  it('makes new tables', () => {
    expect(intType(makeCaps({ flavor: 'sqlite' }))).toBe('INTEGER')
    expect(intType(makeCaps({ flavor: 'mariadb' }))).toBe('int')
    expect(intType(null)).toBe('integer')
    let d = newTableDraft(caps)
    d = { ...d, name: ' orgs ' }
    expect(toTableChange('public', null, d, caps).change).toMatchObject({
      create: true,
      table: 'orgs',
      comment: null,
      primaryKey: ['id'],
      columns: [{ op: 'add', name: 'id', type: { text: 'integer', enumValues: null }, nullable: false, autoIncrement: true }],
    })
    d = { ...togglePK(d, 'n:1'), comment: 'c' }
    expect(toTableChange('public', null, d, caps).change).toMatchObject({ primaryKey: null, comment: 'c' })
    expect(hasChanges(toTableChange('public', null, d, caps).change)).toBe(true)
  })

  it('finds the row a failure is about', () => {
    const origins = { columns: ['c:a'], indexes: ['i:x'], foreignKeys: [], checks: [], triggers: [] }
    const f = (section: string, index: number) => ({ kind: 'invalid' as const, section: section as never, index, field: '', message: '', sql: '', applied: 0 })
    expect(failureKey(f('columns', 0), origins)).toBe('c:a')
    expect(failureKey(f('indexes', 0), origins)).toBe('i:x')
    expect(failureKey(f('table', -1), origins)).toBe('table')
    expect(failureKey(f('primaryKey', -1), origins)).toBe('pk')
    expect(failureKey(f('checks', 3), origins)).toBeNull()
    expect(failureKey(f('other', 0), origins)).toBeNull()
  })

  it('lists live columns and names', () => {
    const d = toggleDropColumn(draftFromDetail(editableDetail(), caps), 'c:org_id')
    expect(liveColumns(d).map((c) => c.name)).toEqual(['id', 'email', 'total'])
    expect(columnName(d, 'c:email')).toBe('email')
    expect(columnName(d, 'nope')).toBe('')
  })
})
