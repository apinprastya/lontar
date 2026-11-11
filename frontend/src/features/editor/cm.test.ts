import { CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { MariaSQL, MySQL, PostgreSQL, SQLite, StandardSQL, schemaCompletionSource, sql } from '@codemirror/lang-sql'
import { EditorState } from '@codemirror/state'
import { EditorView, runScopeHandlers } from '@codemirror/view'
import { describe, expect, it, vi } from 'vitest'
import type { Kind } from '../connections/types'
import { errorField, errorRange, lazyColumnsSource, runKeymap, setErrorMark, setStatement, sqlConfig, sqlDialect, statementField, toSqlNamespace } from './cm'
import type { CompletionSchema } from './types'

const schema: CompletionSchema = {
  database: 'shop',
  schema: 'public',
  schemas: ['public', 'audit'],
  tables: [
    { name: 'users', type: 'table', columns: [{ name: 'id', type: 'int4' }, { name: 'email', type: 'text' }] },
    { name: 'v_users', type: 'view', columns: [{ name: 'email', type: 'text' }] },
    { name: 'big', type: 'table', columns: null },
  ],
  columnsLoaded: true,
}

async function complete(doc: string, cs: CompletionSchema | null = schema): Promise<CompletionResult | null> {
  const config = sqlConfig('postgres', cs)
  const state = EditorState.create({ doc, extensions: [sql(config)] })
  const ctx = new CompletionContext(state, doc.length, true)
  return schemaCompletionSource(config)(ctx) as CompletionResult | null
}

const labels = (r: CompletionResult | null) => (r?.options ?? []).map((o) => o.label)

describe('dialect and schema mapping', () => {
  it('maps kinds to lang-sql dialects', () => {
    expect(sqlDialect('postgres')).toBe(PostgreSQL)
    expect(sqlDialect('mysql')).toBe(MySQL)
    expect(sqlDialect('mariadb')).toBe(MariaSQL)
    expect(sqlDialect('sqlite')).toBe(SQLite)
    expect(sqlDialect('oracle' as Kind)).toBe(StandardSQL)
  })

  it('maps CompletionSchema onto a namespace', () => {
    expect(toSqlNamespace(null)).toEqual({})
    const ns = toSqlNamespace(schema) as Record<string, { self: { label: string; type: string }; children: unknown }>
    expect(Object.keys(ns)).toEqual(['audit', 'public'])
    expect(ns.audit.self).toEqual({ label: 'audit', type: 'namespace' })
    const tables = ns.public.children as Record<string, { self: { label: string; type: string; detail: string }; children: { label: string; detail: string }[] }>
    expect(tables.users.self).toEqual({ label: 'users', type: 'class', detail: 'table' })
    expect(tables.v_users.self.type).toBe('interface')
    expect(tables.users.children).toEqual([
      { label: 'id', type: 'property', detail: 'int4' },
      { label: 'email', type: 'property', detail: 'text' },
    ])
    expect(tables.big.children).toEqual([])
    expect(sqlConfig('postgres', schema)).toMatchObject({ defaultSchema: 'public', upperCaseKeywords: true })
  })

  it('completes tables, qualified columns and aliases', async () => {
    expect(labels(await complete('SELECT * FROM us'))).toEqual(expect.arrayContaining(['users', 'v_users', 'public', 'audit']))
    expect(labels(await complete('SELECT users.'))).toEqual(['id', 'email'])
    expect(labels(await complete('SELECT u. FROM users u'.slice(0, 9)))).toEqual([]) // alias unknown before FROM
    expect(labels(await complete('SELECT * FROM public.'))).toEqual(expect.arrayContaining(['users', 'big']))
    const aliased = 'SELECT * FROM users u WHERE u.'
    expect(labels(await complete(aliased))).toEqual(['id', 'email'])
  })

  it('asks for missing columns of big schemas only', () => {
    const need = vi.fn()
    const big = { ...schema, columnsLoaded: false }
    const ctx = (doc: string) => new CompletionContext(EditorState.create({ doc }), doc.length, false)
    const src = lazyColumnsSource(() => big, need)
    expect(src(ctx('SELECT big.'))).toBeNull()
    expect(need).toHaveBeenCalledWith('big')
    src(ctx('SELECT users.'))
    src(ctx('SELECT x'))
    expect(need).toHaveBeenCalledTimes(1)
    lazyColumnsSource(() => schema, need)(ctx('SELECT big.'))
    lazyColumnsSource(() => null, need)(ctx('SELECT big.'))
    expect(need).toHaveBeenCalledTimes(1)
  })
})

describe('decorations', () => {
  const decos = (state: EditorState, field: typeof statementField) => {
    const out: [number, number][] = []
    state.field(field).between(0, state.doc.length, (from, to) => {
      out.push([from, to])
    })
    return out
  }

  it('highlights the current statement and maps it through edits', () => {
    let state = EditorState.create({ doc: 'SELECT 1; SELECT 2;', extensions: [statementField] })
    state = state.update({ effects: setStatement.of({ from: 10, to: 18 }) }).state
    expect(decos(state, statementField)).toEqual([[10, 18]])
    state = state.update({ changes: { from: 0, insert: '-- x\n' } }).state
    expect(decos(state, statementField)).toEqual([[15, 23]])
    state = state.update({ effects: setStatement.of({ from: 5, to: 999 }) }).state
    expect(decos(state, statementField)).toEqual([[5, state.doc.length]])
    state = state.update({ effects: setStatement.of({ from: 3, to: 3 }) }).state
    expect(decos(state, statementField)).toEqual([])
    state = state.update({ effects: setStatement.of(null) }).state
    expect(decos(state, statementField)).toEqual([])
  })

  it('marks the word at an error until the next edit', () => {
    let state = EditorState.create({ doc: 'SELECT nme FROM t', extensions: [errorField] })
    expect(errorRange(state, 8)).toEqual({ from: 7, to: 10 })
    expect(errorRange(state, 6)).toEqual({ from: 6, to: 7 }) // a space
    expect(errorRange(state, 999)).toEqual({ from: 16, to: 17 })
    expect(errorRange(EditorState.create({ doc: '' }), 0)).toBeNull()
    state = state.update({ effects: setErrorMark.of(7) }).state
    expect(decos(state, errorField)).toEqual([[7, 10]])
    state = state.update({ selection: { anchor: 1 } }).state
    expect(decos(state, errorField)).toEqual([[7, 10]])
    state = state.update({ changes: { from: 0, insert: ' ' } }).state
    expect(decos(state, errorField)).toEqual([])
    state = state.update({ effects: setErrorMark.of(null) }).state
    expect(decos(state, errorField)).toEqual([])
    expect(decos(EditorState.create({ doc: '', extensions: [errorField] }).update({ effects: setErrorMark.of(0) }).state, errorField)).toEqual([])
  })
})

describe('run keys', () => {
  it('runs, runs all and cancels from the keyboard', () => {
    const run = vi.fn()
    let running = false
    const cancel = vi.fn(() => running)
    const view = new EditorView({ state: EditorState.create({ doc: 'SELECT 1', extensions: [runKeymap(() => ({ run, cancel }))] }) })
    const key = (k: string, mods: Partial<KeyboardEvent> = {}) =>
      runScopeHandlers(view, new KeyboardEvent('keydown', { key: k, ctrlKey: true, metaKey: false, ...mods }), 'editor')
    expect(key('Enter')).toBe(true)
    expect(run).toHaveBeenLastCalledWith('current')
    expect(key('Enter', { shiftKey: true })).toBe(true)
    expect(run).toHaveBeenLastCalledWith('all')
    expect(key('Escape', { ctrlKey: false })).toBe(false) // nothing running
    running = true
    expect(key('Escape', { ctrlKey: false })).toBe(true)
    expect(cancel).toHaveBeenCalledTimes(2)
    view.destroy()
  })
})
