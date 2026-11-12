import { describe, expect, it } from 'vitest'
import type { Cell, ResultColumn } from '../features/workspace/types'
import {
  UNDO_LIMIT,
  buildChangeSet,
  cellEditText,
  deleteRows,
  displayRows,
  duplicateValues,
  editValueCell,
  emptyPending,
  failedChange,
  insertRow,
  isDirty,
  pendingCount,
  redo,
  rowKey,
  sameValue,
  setCells,
  stagedValue,
  undo,
  type Pending,
} from './pending'

const cols: ResultColumn[] = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'name', type: 'text', isPK: false, nullable: true },
  { name: 'data', type: 'bytea', isPK: false, nullable: true },
]
const rows: Cell[][] = [
  [{ k: 'num', v: 1 }, { k: 'str', v: 'a' }, { k: 'bytes', v: '00ff', len: 2 }],
  [{ k: 'num', v: 2 }, { k: 'null' }, { k: 'bytes', v: 'ab', len: 9999, trunc: true }],
  [{ k: 'num', v: '9007199254740993' }, { k: 'str', v: 'big' }, { k: 'null' }],
]
const row = (index: number) => ({ type: 'row' as const, index })

describe('pending changes', () => {
  it('keys rows by their primary key cells', () => {
    expect(rowKey(cols, rows[0])).toBe('[["num",1]]')
    expect(rowKey(cols, rows[2])).toBe('[["num","9007199254740993"]]')
    expect(rowKey(cols.map((c) => ({ ...c, isPK: false })), rows[0])).toBeNull()
    expect(rowKey(cols, [{ k: 'null' }])).toBeNull()
    expect(rowKey(cols, [{ k: 'str', v: 'x', trunc: true }])).toBeNull()
  })

  it('converts between cells, editor text and staged values', () => {
    expect(cellEditText(undefined)).toBe('')
    expect(cellEditText({ k: 'null' })).toBe('')
    expect(cellEditText({ k: 'bool', v: true })).toBe('true')
    expect(cellEditText({ k: 'bool', v: false })).toBe('false')
    expect(cellEditText({ k: 'num', v: 1.5 })).toBe('1.5')
    expect(cellEditText({ k: 'str' })).toBe('')
    expect(editValueCell({ kind: 'null' })).toEqual({ k: 'null' })
    expect(editValueCell({ kind: 'default' })).toEqual({ k: 'str', v: 'DEFAULT' })
    expect(editValueCell({ kind: 'bytes', hex: 'ABCD' })).toEqual({ k: 'bytes', v: 'abcd', len: 2 })
    expect(editValueCell({ kind: 'value', text: 'x' })).toEqual({ k: 'str', v: 'x' })
    expect(sameValue({ kind: 'null' }, { k: 'null' })).toBe(true)
    expect(sameValue({ kind: 'null' }, undefined)).toBe(false)
    expect(sameValue({ kind: 'default' }, { k: 'null' })).toBe(false)
    expect(sameValue({ kind: 'bytes', hex: '00FF' }, rows[0][2])).toBe(true)
    expect(sameValue({ kind: 'bytes', hex: 'ab' }, rows[1][2])).toBe(false)
    expect(sameValue({ kind: 'value', text: 'a' }, rows[0][1])).toBe(true)
    expect(sameValue({ kind: 'value', text: '' }, { k: 'null' })).toBe(false)
  })

  it('stages edits, drops no-op edits and counts changes', () => {
    let p = setCells(emptyPending, cols, rows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: 'b' } }])
    expect(pendingCount(p)).toBe(1)
    expect(p.edits['[["num",1]]']).toEqual({ pk: { id: { k: 'num', v: 1 } }, old: { name: { k: 'str', v: 'a' } }, set: { name: { kind: 'value', text: 'b' } } })
    // Editing back to the original removes the change.
    p = setCells(p, cols, rows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: 'a' } }])
    expect(isDirty(p)).toBe(false)
    expect(p.undo).toHaveLength(2)
    // Truncated originals, unknown columns and missing rows are skipped.
    const same = setCells(p, cols, rows, [
      { ref: row(1), column: 'data', value: { kind: 'null' } },
      { ref: row(0), column: 'nope', value: { kind: 'null' } },
      { ref: row(9), column: 'name', value: { kind: 'null' } },
    ])
    expect(same).toBe(p)
    // Editing a key column keeps the original key.
    p = setCells(p, cols, rows, [{ ref: row(2), column: 'id', value: { kind: 'value', text: '5' } }])
    expect(buildChangeSet('public', 't', p).cs.updates[0].pk).toEqual({ id: { k: 'num', v: '9007199254740993' } })
  })

  it('inserts, duplicates and deletes rows', () => {
    let { pending: p, id } = insertRow(emptyPending)
    expect(id).toBe(1)
    p = setCells(p, cols, rows, [{ ref: { type: 'insert', id }, column: 'name', value: { kind: 'value', text: 'new' } }])
    ;({ pending: p } = insertRow(p, { name: { kind: 'null' } }))
    expect(p.inserts.map((r) => r.id)).toEqual([1, 2])
    // Deleting a fetched row marks it; a staged insert is dropped.
    p = deleteRows(p, cols, rows, [row(0), { type: 'insert', id: 2 }])
    expect(Object.keys(p.deletes)).toEqual(['[["num",1]]'])
    expect(p.inserts.map((r) => r.id)).toEqual([1])
    expect(pendingCount(p)).toBe(2)
    // Deleting only marked rows again unmarks them.
    p = deleteRows(p, cols, rows, [row(0)])
    expect(p.deletes).toEqual({})
    // Nothing to delete: unchanged.
    expect(deleteRows(p, cols, rows, [row(42)])).toBe(p)
    // A deleted row cannot be edited, and its edits are left out.
    p = setCells(p, cols, rows, [{ ref: row(2), column: 'name', value: { kind: 'value', text: 'x' } }])
    p = deleteRows(p, cols, rows, [row(2)])
    expect(setCells(p, cols, rows, [{ ref: row(2), column: 'name', value: { kind: 'null' } }])).toBe(p)
    const { cs, refs } = buildChangeSet('s', 't', p)
    expect(cs).toEqual({ schema: 's', table: 't', updates: [], deletes: [{ pk: { id: rows[2][0] } }], inserts: [{ values: { name: { kind: 'value', text: 'new' } } }] })
    expect(refs).toEqual({ updates: [], deletes: ['[["num","9007199254740993"]]'], inserts: [1] })
    expect(pendingCount(p)).toBe(2)
  })

  it('undoes and redoes, and caps the history', () => {
    let p: Pending = emptyPending
    p = setCells(p, cols, rows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: '1' } }])
    p = setCells(p, cols, rows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: '2' } }])
    p = undo(p)
    expect(p.edits['[["num",1]]'].set.name).toEqual({ kind: 'value', text: '1' })
    p = undo(p)
    expect(isDirty(p)).toBe(false)
    expect(undo(p)).toBe(p)
    p = redo(redo(p))
    expect(p.edits['[["num",1]]'].set.name).toEqual({ kind: 'value', text: '2' })
    expect(redo(p)).toBe(p)
    // A new change clears redo.
    p = undo(p)
    p = insertRow(p).pending
    expect(p.redo).toEqual([])
    for (let i = 0; i < UNDO_LIMIT + 10; i++) p = insertRow(p).pending
    expect(p.undo).toHaveLength(UNDO_LIMIT)
  })

  it('maps failures to staged changes', () => {
    const refs = { updates: ['u'], inserts: [7], deletes: ['d'] }
    expect(failedChange(refs, null)).toBeNull()
    expect(failedChange(refs, { index: -1, kind: 'database', message: '' })).toBeNull()
    expect(failedChange(refs, { op: 'update', index: 0, kind: 'stale', message: '' })).toEqual({ key: 'u' })
    expect(failedChange(refs, { op: 'delete', index: 0, kind: 'stale', message: '' })).toEqual({ key: 'd' })
    expect(failedChange(refs, { op: 'insert', index: 0, kind: 'database', message: '' })).toEqual({ insertId: 7 })
    expect(failedChange(refs, { op: 'insert', index: 3, kind: 'database', message: '' })).toBeNull()
  })

  it('builds display rows with visual state', () => {
    let p = setCells(emptyPending, cols, rows, [
      { ref: row(0), column: 'name', value: { kind: 'default' } },
    ])
    p = deleteRows(p, cols, rows, [row(2)])
    p = insertRow(p, { name: { kind: 'value', text: 'n' } }).pending
    const d = displayRows(cols, rows, p)
    expect(d).toHaveLength(4)
    expect(d[0]).toMatchObject({ state: 'normal', modified: [false, true, false], defaults: [false, true, false], locked: [false, false, false] })
    expect(d[0].cells[1]).toEqual({ k: 'str', v: 'DEFAULT' })
    expect(d[1].locked).toEqual([false, false, true]) // truncated bytes
    expect(d[2].state).toBe('deleted')
    expect(d[3]).toMatchObject({ state: 'inserted', key: null, ref: { type: 'insert', id: 1 }, defaults: [true, false, true], modified: [false, true, false] })
    expect(d[3].cells[1]).toEqual({ k: 'str', v: 'n' })
    expect(stagedValue(p, d[0], 'name')).toEqual({ kind: 'default' })
    expect(stagedValue(p, d[3], 'name')).toEqual({ kind: 'value', text: 'n' })
    expect(stagedValue(p, { ...d[1], key: null }, 'name')).toBeUndefined()
    // Unkeyed rows are locked.
    const unkeyed = displayRows(cols, [[{ k: 'null' }, { k: 'str', v: 'x' }, { k: 'null' }]], emptyPending)
    expect(unkeyed[0].locked).toEqual([true, true, true])
  })

  it('duplicates values without the key', () => {
    const p = setCells(emptyPending, cols, rows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: 'staged' } }])
    const d = displayRows(cols, rows, p)
    expect(duplicateValues(cols, d[0], p)).toEqual({ name: { kind: 'value', text: 'staged' }, data: { kind: 'bytes', hex: '00ff' } })
    // NULL stays NULL; truncated values are left to the default.
    expect(duplicateValues(cols, d[1], p)).toEqual({ name: { kind: 'null' } })
  })
})

describe('query result columns', () => {
  // `SELECT id AS key, name, upper(name) AS shout FROM t`
  const qcols: ResultColumn[] = [
    { name: 'key', type: 'integer', isPK: true, nullable: false },
    { name: 'name', type: 'text', isPK: false, nullable: true },
    { name: 'shout', type: 'text', isPK: false, nullable: true, readOnly: true },
  ]
  const qrows: Cell[][] = [[{ k: 'num', v: 1 }, { k: 'str', v: 'a' }, { k: 'str', v: 'A' }]]

  it('locks read-only columns and leaves them out of changes', () => {
    let p = setCells(emptyPending, qcols, qrows, [
      { ref: row(0), column: 'shout', value: { kind: 'value', text: 'x' } },
      { ref: row(0), column: 'name', value: { kind: 'value', text: 'b' } },
    ])
    p = insertRow(p, {}).pending
    const d = displayRows(qcols, qrows, p)
    expect(d[0].locked).toEqual([false, false, true])
    expect(d[0].cells[2]).toEqual({ k: 'str', v: 'A' })
    expect(d[1]).toMatchObject({ locked: [false, false, true], defaults: [true, true, false], modified: [false, false, false] })
    expect(d[1].cells[2]).toEqual({ k: 'str', v: '' })
    expect(duplicateValues(qcols, d[0], p)).toEqual({ name: { kind: 'value', text: 'b' } })
  })

  it('renames result columns to table columns in the change set', () => {
    let p = setCells(emptyPending, qcols, qrows, [{ ref: row(0), column: 'name', value: { kind: 'value', text: 'b' } }])
    p = insertRow(p, { key: { kind: 'value', text: '5' } }).pending
    p = deleteRows(p, qcols, [...qrows, [{ k: 'num', v: 2 }, { k: 'null' }, { k: 'null' }]], [row(1)])
    const { cs } = buildChangeSet('public', 't', p, { key: 'id', name: 'name' })
    expect(cs.updates[0]).toEqual({ pk: { id: { k: 'num', v: 1 } }, set: { name: { kind: 'value', text: 'b' } }, old: { name: { k: 'str', v: 'a' } } })
    expect(cs.inserts[0]).toEqual({ values: { id: { kind: 'value', text: '5' } } })
    expect(cs.deletes[0]).toEqual({ pk: { id: { k: 'num', v: 2 } } })
  })
})
