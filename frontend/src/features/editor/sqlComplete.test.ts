import { CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { PostgreSQL, keywordCompletionSource } from '@codemirror/lang-sql'
import { LanguageSupport } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { describe, expect, it, vi } from 'vitest'
import type { Kind } from '../connections/types'
import { buildOptions, completionSource, contextAt, quoteIdent, resolveQualifier, statementAt, tableRefs, tokenize } from './sqlComplete'
import type { CompletionSchema } from './types'

const schema: CompletionSchema = {
  database: 'shop',
  schema: 'public',
  schemas: ['public', 'audit'],
  tables: [
    { name: 'users', type: 'table', columns: [{ name: 'id', type: 'int4' }, { name: 'email', type: 'text' }, { name: 'name', type: 'text' }] },
    { name: 'orders', type: 'table', columns: [{ name: 'id', type: 'int4' }, { name: 'user_id', type: 'int4' }, { name: 'total', type: 'numeric' }] },
    { name: 'v_active', type: 'view', columns: [{ name: 'email', type: 'text' }] },
    { name: 'Big Table', type: 'table', columns: null },
  ],
  columnsLoaded: false,
}

/**
 * Completes at "|" in doc (or at the end). explicit mimics Ctrl+Space.
 */
async function complete(doc: string, opts: { explicit?: boolean; cs?: CompletionSchema | null; kind?: Kind; need?: (t: string) => void } = {}) {
  const pos = doc.includes('|') ? doc.indexOf('|') : doc.length
  const text = doc.replace('|', '')
  const kind = opts.kind ?? 'postgres'
  const state = EditorState.create({ doc: text, extensions: [new LanguageSupport(PostgreSQL.language)] })
  const src = completionSource({
    kind,
    schema: () => (opts.cs === undefined ? schema : opts.cs),
    need: opts.need ?? (() => {}),
    keywords: keywordCompletionSource(PostgreSQL, true),
  })
  return (await src(new CompletionContext(state, pos, opts.explicit ?? false))) as CompletionResult | null
}

/** Labels of tables, columns, schemas and aliases (no keywords), best first. */
const names = (r: CompletionResult | null) =>
  (r?.options ?? [])
    .filter((o) => (o.boost ?? 0) >= 0 && ['class', 'interface', 'property', 'namespace', 'variable'].includes(o.type ?? ''))
    .sort((a, b) => (b.boost ?? 0) - (a.boost ?? 0))
    .map((o) => o.label)
/** Column labels only, best first. */
const columns = (r: CompletionResult | null) => names(r).filter((l) => r?.options.find((o) => o.label === l)?.type === 'property')
const hasKeyword = (r: CompletionResult | null, kw: string) => (r?.options ?? []).some((o) => o.label === kw && o.type === 'keyword')

describe('the UPDATE example', () => {
  it('completes the table after UPDATE, even before typing', async () => {
    const r = await complete('UPDATE |')
    expect(names(r)).toEqual(expect.arrayContaining(['users', 'orders', 'v_active']))
    expect(names(r)).toContain('audit') // another schema
    expect(hasKeyword(r, 'SELECT')).toBe(false)
    expect(names(await complete('UPDATE us'))).toContain('users')
  })

  it("completes the table's columns after SET and WHERE", async () => {
    expect(names(await complete("UPDATE users SET |"))).toEqual([])  // not before typing (no popup on space)
    expect(columns(await complete('UPDATE users SET |', { explicit: true }))).toEqual(['id', 'email', 'name'])
    const set = await complete('UPDATE users SET em')
    expect(columns(set)).toEqual(['id', 'email', 'name'])
    expect(set?.from).toBe('UPDATE users SET '.length)
    expect(columns(await complete("UPDATE users SET email = '' WHERE na"))).toEqual(['id', 'email', 'name'])
    expect(columns(await complete("UPDATE users SET email = 'x', na| WHERE id = 1"))).toEqual(['id', 'email', 'name'])
  })

  it('does not complete inside strings and comments', async () => {
    expect(await complete("UPDATE users SET email = 'na")).toBeNull()
    expect(await complete("UPDATE users SET email = 'na|'")).toBeNull()
    expect(await complete('UPDATE users -- na')).toBeNull()
    expect(await complete('UPDATE users /* na */ SET n')).not.toBeNull()
  })
})

describe('aliases and qualified names', () => {
  it('completes alias.column, with the FROM after the cursor', async () => {
    expect(names(await complete('SELECT u.| FROM users u'))).toEqual(['id', 'email', 'name'])
    expect(names(await complete('SELECT u.em| FROM users AS u'))).toEqual(['id', 'email', 'name'])
    expect(names(await complete('SELECT o.| FROM users u JOIN orders o ON o.user_id = u.id'))).toEqual(['id', 'user_id', 'total'])
    expect(names(await complete('SELECT * FROM users u JOIN orders o ON o.'))).toEqual(['id', 'user_id', 'total'])
    // A table name works as a qualifier too, aliased or not.
    expect(names(await complete('SELECT users.| FROM users'))).toEqual(['id', 'email', 'name'])
    expect(names(await complete('SELECT orders.'))).toEqual(['id', 'user_id', 'total'])
    // public.<cursor>: tables of the schema; public.users.<cursor>: columns.
    expect(names(await complete('SELECT * FROM public.'))).toEqual(expect.arrayContaining(['users', 'orders']))
    expect(names(await complete('SELECT public.users.'))).toEqual(['id', 'email', 'name'])
    expect(await complete('SELECT nope.')).toBeNull()
  })

  it('offers the columns of every table in a join, qualifying shared names', async () => {
    const r = await complete('SELECT | FROM users u JOIN orders o ON o.user_id = u.id', { explicit: true })
    const labels = names(r)
    expect(labels).toEqual(expect.arrayContaining(['u.id', 'o.id', 'email', 'name', 'user_id', 'total', 'u', 'o']))
    expect(labels).not.toContain('id')
    const email = r?.options.find((o) => o.label === 'email')
    expect(email?.detail).toBe('users · text')
    const uid = r?.options.find((o) => o.label === 'u.id')
    expect(uid).toMatchObject({ apply: 'u.id', detail: 'int4' })
    expect(r?.options.find((o) => o.label === 'u')?.detail).toBe('alias of users')
    // Column positions after WHERE / ON / ORDER BY / GROUP BY.
    expect(names(await complete('SELECT * FROM users WHERE id = 1 AND na'))).toContain('name')
    expect(names(await complete('SELECT * FROM users ORDER BY na'))).toContain('name')
    expect(names(await complete('SELECT * FROM users, orders WHERE to'))).toContain('total')
  })

  it('keeps keywords below columns', async () => {
    const r = await complete('SELECT * FROM users WHERE n')
    expect(hasKeyword(r, 'NOT')).toBe(true)
    const kw = r!.options.find((o) => o.label === 'NOT')!
    const col = r!.options.find((o) => o.label === 'name')!
    expect((col.boost ?? 0) > (kw.boost ?? 0)).toBe(true)
  })
})

describe('other statements', () => {
  it('completes tables after FROM, JOIN, INTO, DELETE FROM and in FROM lists', async () => {
    for (const doc of ['SELECT * FROM o', 'SELECT * FROM users JOIN o', 'INSERT INTO o', 'DELETE FROM o', 'SELECT * FROM users, o', 'TRUNCATE o']) {
      expect(names(await complete(doc))).toContain('orders')
    }
  })

  it('offers keywords, not columns, right after a table name', async () => {
    const r = await complete('SELECT * FROM users WH')
    expect(names(r)).toEqual([])
    expect(hasKeyword(r, 'WHERE')).toBe(true)
  })

  it("completes the columns of INSERT INTO t (…)", async () => {
    expect(names(await complete('INSERT INTO orders (to'))).toEqual(['id', 'user_id', 'total'])
    expect(names(await complete('INSERT INTO public.orders (id, us'))).toEqual(['id', 'user_id', 'total'])
    expect(names(await complete("INSERT INTO orders (id) VALUES (1, u"))).toEqual(expect.arrayContaining(['users']))
  })

  it('looks only at the statement under the cursor', async () => {
    expect(columns(await complete('SELECT * FROM orders; SELECT * FROM users WHERE na'))).toEqual(['id', 'email', 'name'])
    expect(columns(await complete('SELECT * FROM orders WHERE to|; SELECT * FROM users'))).toEqual(['id', 'user_id', 'total'])
  })

  it('offers tables before a FROM exists, and keywords on explicit request', async () => {
    expect(names(await complete('SELECT us'))).toContain('users')
    expect(names(await complete('SEL'))).toContain('users')
    expect(await complete('SELECT * FROM users ')).toBeNull() // a space alone opens nothing
    expect(await complete('SELECT 1', { cs: null })).not.toBeNull() // keywords still work
  })
})

describe('big schemas and quoting', () => {
  it('asks for columns that are not loaded yet', async () => {
    const need = vi.fn()
    expect(await complete('SELECT * FROM "Big Table" b WHERE b.', { need })).toBeNull()
    expect(need).toHaveBeenCalledWith('Big Table')
    need.mockClear()
    await complete('SELECT * FROM "Big Table" WHERE x', { need })
    expect(need).toHaveBeenCalledWith('Big Table')
  })

  it('quotes names that need it', async () => {
    const r = await complete('SELECT * FROM Bi')
    expect(r?.options.find((o) => o.label === 'Big Table')?.apply).toBe('"Big Table"')
    expect(quoteIdent('postgres', 'users')).toBe('users')
    expect(quoteIdent('postgres', 'Users')).toBe('"Users"')
    expect(quoteIdent('postgres', 'a"b')).toBe('"a""b"')
    expect(quoteIdent('mysql', 'Users')).toBe('Users')
    expect(quoteIdent('mysql', 'my table')).toBe('`my table`')
    expect(quoteIdent('sqlite', 'my table')).toBe('"my table"')
  })

  it('matches names as the engine does', () => {
    expect(resolveQualifier('postgres', schema, [], 'USERS')?.name).toBe('users')
    expect(resolveQualifier('mysql', schema, [], 'Orders')?.name).toBe('orders')
    expect(resolveQualifier('postgres', schema, [{ schema: 'audit', table: 'users', alias: 'a' }], 'a')).toBeNull()
  })
})

describe('tokenizer and parsing helpers', () => {
  it('tokenizes dialect quoting and comments', () => {
    const kinds = (k: Kind, s: string) => tokenize(k, s).map((t) => `${t.kind}:${t.text}`)
    expect(kinds('mysql', 'SELECT `a b`, "s" # c')).toEqual(['word:SELECT', 'quoted:a b', 'punct:,', 'string:"s"', 'comment:# c'])
    expect(kinds('sqlite', 'SELECT [x y] FROM t')).toEqual(['word:SELECT', 'quoted:x y', 'word:FROM', 'word:t'])
    expect(kinds('postgres', 'SELECT $$a;b$$, $t$x$t$, 1.5, "q""x"')).toEqual(['word:SELECT', 'string:$$a;b$$', 'punct:,', 'string:$t$x$t$', 'punct:,', 'number:1.5', 'punct:,', 'quoted:q"x'])
    expect(tokenize('postgres', "'open").slice(-1)[0]?.open).toBe(true)
    expect(tokenize('postgres', '/* open').slice(-1)[0]?.open).toBe(true)
    expect(tokenize('postgres', '"open').slice(-1)[0]?.open).toBe(true)
    expect(tokenize('postgres', '$$open').slice(-1)[0]?.open).toBe(true)
    expect(tokenize('mysql', "'a\\'b'")).toHaveLength(1)
  })

  it('finds statements and table references', () => {
    const toks = tokenize('postgres', 'SELECT 1; SELECT * FROM ONLY a x, s.b AS y LEFT JOIN c ON true; UPDATE d SET')
    expect(statementAt(toks, 3).map((t) => t.text)).toEqual(['SELECT', '1'])
    const second = statementAt(toks, 15)
    expect(tableRefs(second)).toEqual([
      { schema: null, table: 'a', alias: 'x' },
      { schema: 's', table: 'b', alias: 'y' },
      { schema: null, table: 'c', alias: null },
    ])
    expect(tableRefs(statementAt(toks, toks.slice(-1)[0]!.to))).toEqual([{ schema: null, table: 'd', alias: null }])
    expect(tableRefs(tokenize('postgres', 'SELECT * FROM WHERE'))).toEqual([])
  })

  it('classifies cursor positions', () => {
    const at = (s: string) => contextAt(tokenize('postgres', s))
    expect(at('SELECT * FROM')).toEqual({ type: 'table', schema: null })
    expect(at('SELECT * FROM a,')).toEqual({ type: 'table', schema: null })
    expect(at('SELECT * FROM a')).toEqual({ type: 'afterTable' })
    expect(at('SELECT * FROM s.a')).toEqual({ type: 'afterTable' })
    expect(at('SELECT * FROM a, b')).toEqual({ type: 'afterTable' })
    expect(at('SELECT a.')).toEqual({ type: 'member', qualifier: 'a', schema: null })
    expect(at('SELECT s.a.')).toEqual({ type: 'member', qualifier: 'a', schema: 's' })
    expect(at('SELECT * FROM a WHERE (x =')).toEqual({ type: 'column', only: null })
    expect(at('SELECT * FROM a WHERE x IN (SELECT')).toEqual({ type: 'column', only: null })
    expect(at('SELECT * FROM a LIMIT')).toEqual({ type: 'other' })
    expect(at('SELECT max(')).toEqual({ type: 'column', only: null })
    expect(at('INSERT INTO t (a) VALUES (')).toEqual({ type: 'other' })
  })

  it('builds nothing without completion data', () => {
    expect(buildOptions('postgres', null, [], { type: 'table', schema: null })).toEqual({ options: [], keywords: true, missing: [] })
  })
})
