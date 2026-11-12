/**
 * Context-aware SQL completion for the editor: tables where a table goes,
 * the columns of the statement's tables where a column goes, and the
 * columns of the table an alias names after "alias.".
 *
 * It works on plain text (a light tokenizer, no parser), so it copes with
 * the half-written statements completion is used on. Everything here is
 * pure and unit-testable; the CodeMirror glue is completionSource().
 */
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from '@codemirror/autocomplete'
import type { Kind } from '../connections/types'
import type { CompletionSchema, CompletionTable } from './types'

// ---- Tokens ----------------------------------------------------------------

export type TokKind = 'word' | 'quoted' | 'string' | 'number' | 'punct' | 'comment'

export interface Tok {
  kind: TokKind
  /** Identifier text (unquoted for quoted identifiers), else the raw text. */
  text: string
  /** Upper-cased text of a word. */
  up: string
  from: number
  to: number
  /** A string, comment or quoted identifier that is not closed. */
  open?: boolean
}

/** Splits SQL into tokens (comments included, whitespace dropped). */
export function tokenize(kind: Kind, s: string): Tok[] {
  const out: Tok[] = []
  const push = (k: TokKind, from: number, to: number, text = s.slice(from, to), open = false) =>
    out.push({ kind: k, text, up: k === 'word' ? text.toUpperCase() : text, from, to, ...(open ? { open } : {}) })
  /** Index after the closing quote, or -1 when it is not closed. */
  const closeQuote = (i: number, q: string, backslash: boolean): number => {
    for (let j = i + 1; j < s.length; j++) {
      if (backslash && s[j] === '\\') j++
      else if (s[j] === q) {
        if (s[j + 1] === q && q !== ']') j++
        else return j + 1
      }
    }
    return -1
  }
  let i = 0
  while (i < s.length) {
    const c = s[i]
    if (c <= ' ') {
      i++
    } else if ((c === '-' && s[i + 1] === '-') || (c === '#' && kind !== 'postgres' && kind !== 'sqlite')) {
      const end = s.indexOf('\n', i)
      const to = end < 0 ? s.length : end
      push('comment', i, to, s.slice(i, to), end < 0)
      i = to
    } else if (c === '/' && s[i + 1] === '*') {
      const end = s.indexOf('*/', i + 2)
      const to = end < 0 ? s.length : end + 2
      push('comment', i, to, s.slice(i, to), end < 0)
      i = to
    } else if (c === "'" || (c === '"' && (kind === 'mysql' || kind === 'mariadb'))) {
      const end = closeQuote(i, c, kind === 'mysql' || kind === 'mariadb')
      const to = end < 0 ? s.length : end
      push('string', i, to, s.slice(i, to), end < 0)
      i = to
    } else if (c === '"' || c === '`' || (c === '[' && kind === 'sqlite')) {
      const q = c === '[' ? ']' : c
      const end = closeQuote(i, q, false)
      const to = end < 0 ? s.length : end
      const inner = s.slice(i + 1, end < 0 ? s.length : end - 1)
      push('quoted', i, to, q === ']' ? inner : inner.split(q + q).join(q), end < 0)
      i = to
    } else if (c === '$' && kind === 'postgres' && /^\$([A-Za-z_]\w*)?\$/.test(s.slice(i))) {
      const tag = /^\$([A-Za-z_]\w*)?\$/.exec(s.slice(i))![0]
      const end = s.indexOf(tag, i + tag.length)
      const to = end < 0 ? s.length : end + tag.length
      push('string', i, to, s.slice(i, to), end < 0)
      i = to
    } else if (/[A-Za-z_\u0080-￿]/.test(c)) {
      let j = i + 1
      while (j < s.length && /[\w$\u0080-￿]/.test(s[j])) j++
      push('word', i, j)
      i = j
    } else if (/[0-9]/.test(c)) {
      let j = i + 1
      while (j < s.length && /[\w.]/.test(s[j])) j++
      push('number', i, j)
      i = j
    } else {
      push('punct', i, i + 1)
      i++
    }
  }
  return out
}

/** The tokens of the statement containing pos (split at top-level ";"). */
export function statementAt(toks: Tok[], pos: number): Tok[] {
  let start = 0
  for (let k = 0; k < toks.length; k++) {
    const t = toks[k]
    if (t.kind === 'punct' && t.text === ';') {
      if (t.to <= pos) start = k + 1
      else return toks.slice(start, k)
    }
  }
  return toks.slice(start)
}

// ---- Table references -------------------------------------------------------

export interface TableRef {
  schema: string | null
  table: string
  alias: string | null
}

/** Keywords that are followed by a table name. */
const TABLE_KEYWORDS = new Set(['FROM', 'JOIN', 'UPDATE', 'INTO', 'TABLE', 'TRUNCATE'])

/** Words that end a table reference instead of being its alias. */
const NOT_ALIAS = new Set([
  'WHERE', 'SET', 'ON', 'USING', 'JOIN', 'INNER', 'LEFT', 'RIGHT', 'FULL', 'OUTER', 'CROSS', 'NATURAL', 'LATERAL',
  'GROUP', 'ORDER', 'HAVING', 'LIMIT', 'OFFSET', 'FETCH', 'FOR', 'UNION', 'INTERSECT', 'EXCEPT', 'WINDOW', 'VALUES',
  'SELECT', 'RETURNING', 'DEFAULT', 'AS', 'ONLY', 'TABLESAMPLE', 'USE', 'FORCE', 'IGNORE', 'PARTITION', 'WITH',
])

const isName = (t: Tok | undefined): t is Tok => Boolean(t && (t.kind === 'word' || t.kind === 'quoted'))
const isPunct = (t: Tok | undefined, p: string) => Boolean(t && t.kind === 'punct' && t.text === p)

/** Reads `[schema.]table [[AS] alias]` at k; returns the ref and the index after it. */
function readRef(toks: Tok[], k: number): { ref: TableRef; next: number } | null {
  let a = toks[k]
  // PostgreSQL: FROM ONLY t
  if (a?.kind === 'word' && a.up === 'ONLY') a = toks[++k]
  if (!isName(a) || (a.kind === 'word' && NOT_ALIAS.has(a.up))) return null
  let schema: string | null = null
  let table = a.text
  let n = k + 1
  if (isPunct(toks[n], '.') && isName(toks[n + 1])) {
    schema = table
    table = toks[n + 1].text
    n += 2
  }
  let alias: string | null = null
  if (toks[n]?.kind === 'word' && toks[n].up === 'AS' && isName(toks[n + 1])) {
    alias = toks[n + 1].text
    n += 2
  } else if (isName(toks[n]) && !(toks[n].kind === 'word' && NOT_ALIAS.has(toks[n].up))) {
    alias = toks[n].text
    n += 1
  }
  return { ref: { schema, table, alias }, next: n }
}

/** Every table a statement names (FROM/JOIN lists, UPDATE, INSERT INTO, DELETE FROM). */
export function tableRefs(toks: Tok[]): TableRef[] {
  const refs: TableRef[] = []
  const code = toks.filter((t) => t.kind !== 'comment')
  for (let k = 0; k < code.length; k++) {
    const t = code[k]
    if (t.kind !== 'word' || !TABLE_KEYWORDS.has(t.up)) continue
    // ALTER/DROP/CREATE TABLE x: a table; but "TABLE" in RETURNS TABLE (...) is not.
    let r = readRef(code, k + 1)
    while (r) {
      refs.push(r.ref)
      // FROM a, b, c
      if (t.up !== 'FROM' || !isPunct(code[r.next], ',')) break
      r = readRef(code, r.next + 1)
    }
  }
  return refs
}

// ---- Context at the cursor ---------------------------------------------------

export type CompletionContextKind =
  /** A table name goes here (after FROM, JOIN, UPDATE, INTO, …). */
  | { type: 'table'; schema: string | null }
  /** After "q.": the columns of q's table, or the tables of schema q. */
  | { type: 'member'; qualifier: string; schema: string | null }
  /** A column of the statement's tables goes here. */
  | { type: 'column'; only: TableRef | null }
  /** Right after a table name: an alias or a keyword. */
  | { type: 'afterTable' }
  /** Anything else: keywords and tables. */
  | { type: 'other' }

const CLAUSES: Record<string, string> = {
  SELECT: 'column', WHERE: 'column', SET: 'column', ON: 'column', HAVING: 'column', ORDER: 'column', GROUP: 'column',
  RETURNING: 'column', USING: 'column', AND: '', OR: '', NOT: '', BY: '',
  FROM: 'from', JOIN: 'from', UPDATE: 'from', INTO: 'into', VALUES: 'values', LIMIT: 'other', OFFSET: 'other',
}

/**
 * What goes at the cursor. before are the statement's tokens before the
 * word being typed.
 */
export function contextAt(before: Tok[]): CompletionContextKind {
  const code = before.filter((t) => t.kind !== 'comment')
  const last = code[code.length - 1]
  const prev = code[code.length - 2]

  // q.<cursor> or s.q.<cursor>
  if (isPunct(last, '.') && isName(prev)) {
    const schema = isPunct(code[code.length - 3], '.') && isName(code[code.length - 4]) ? code[code.length - 4].text : null
    return { type: 'member', qualifier: prev.text, schema }
  }

  // Clause of the current parenthesis level, and the innermost open "(".
  let depth = 0
  const clauseAt: string[] = ['']
  const openAt: number[] = []
  code.forEach((t, k) => {
    if (isPunct(t, '(')) {
      depth++
      clauseAt[depth] = ''
      openAt[depth] = k
    } else if (isPunct(t, ')')) {
      depth = Math.max(0, depth - 1)
    } else if (t.kind === 'word' && CLAUSES[t.up] !== undefined && CLAUSES[t.up] !== '') {
      clauseAt[depth] = CLAUSES[t.up]
    }
  })
  const clause = clauseAt[depth] ?? ''

  if (last?.kind === 'word' && TABLE_KEYWORDS.has(last.up)) return { type: 'table', schema: null }
  if (isPunct(last, ',') && clause === 'from') return { type: 'table', schema: null }
  // FROM t <cursor> / UPDATE t <cursor>: an alias or the next keyword.
  if (isName(last)) {
    const before2 = isPunct(prev, '.') ? code[code.length - 4] : prev
    if (before2?.kind === 'word' && TABLE_KEYWORDS.has(before2.up)) return { type: 'afterTable' }
    if (isPunct(before2, ',') && clause === 'from') return { type: 'afterTable' }
  }

  // INSERT INTO t ( <cursor> : t's columns.
  if (depth > 0 && clause === '') {
    const k = openAt[depth]
    let j = k - 1
    if (isName(code[j])) {
      let table = code[j].text
      let schema: string | null = null
      if (isPunct(code[j - 1], '.') && isName(code[j - 2])) {
        schema = code[j - 2].text
        table = code[j].text
        j -= 2
      }
      if (code[j - 1]?.kind === 'word' && code[j - 1].up === 'INTO') return { type: 'column', only: { schema, table, alias: null } }
    }
  }

  // A parenthesis without its own clause (an expression, a function call)
  // belongs to the enclosing one.
  let inherited = clause
  for (let d = depth; inherited === '' && d >= 0; d--) inherited = clauseAt[d] ?? ''
  if (inherited === 'column') return { type: 'column', only: null }
  return { type: 'other' }
}

// ---- Completions ----------------------------------------------------------------

/** Quotes an identifier for insertion when it needs quoting. */
export function quoteIdent(kind: Kind, name: string): string {
  if (kind === 'postgres') return /^[a-z_][a-z0-9_$]*$/.test(name) ? name : `"${name.replace(/"/g, '""')}"`
  if (kind === 'sqlite') return /^[A-Za-z_][\w$]*$/.test(name) ? name : `"${name.replace(/"/g, '""')}"`
  return /^[A-Za-z_][\w$]*$/.test(name) ? name : `\`${name.replace(/`/g, '``')}\``
}

/** Compares identifiers as the engine would (PostgreSQL folds unquoted names to lower case). */
function sameName(kind: Kind, a: string, b: string): boolean {
  return kind === 'postgres' ? a === b || a.toLowerCase() === b : a.toLowerCase() === b.toLowerCase()
}

/** The completion table a reference names (current schema only). */
export function resolveTable(kind: Kind, cs: CompletionSchema, ref: Pick<TableRef, 'schema' | 'table'>): CompletionTable | null {
  if (ref.schema && !sameName(kind, ref.schema, cs.schema)) return null
  return cs.tables.find((t) => sameName(kind, ref.table, t.name)) ?? null
}

/** The table q names in a statement: an alias, else a table name. */
export function resolveQualifier(kind: Kind, cs: CompletionSchema, refs: TableRef[], q: string): CompletionTable | null {
  const byAlias = refs.find((r) => r.alias !== null && sameName(kind, q, r.alias))
  if (byAlias) return resolveTable(kind, cs, byAlias)
  const byName = refs.find((r) => sameName(kind, q, r.table))
  if (byName) return resolveTable(kind, cs, byName)
  return resolveTable(kind, cs, { schema: null, table: q })
}

const tableOption = (kind: Kind, t: CompletionTable, boost: number): Completion => ({
  label: t.name,
  apply: quoteIdent(kind, t.name),
  type: t.type === 'table' ? 'class' : 'interface',
  detail: t.type,
  boost,
})

export interface Built {
  options: Completion[]
  /** Keywords are offered too (lower ranked). */
  keywords: boolean
  /** Tables whose columns must be loaded first (big schemas). */
  missing: string[]
}

/** The completions for a context. */
export function buildOptions(kind: Kind, cs: CompletionSchema | null, statement: Tok[], ctx: CompletionContextKind): Built {
  const none: Built = { options: [], keywords: true, missing: [] }
  if (!cs) return none
  const refs = tableRefs(statement)
  const missing: string[] = []
  /** A table's columns; detail names the table when several are in play. */
  const cols = (t: CompletionTable, boost: number, opts: { prefix?: string | null; showTable?: boolean; only?: string } = {}): Completion[] => {
    if (t.columns === null) {
      missing.push(t.name)
      return []
    }
    return t.columns
      .filter((c) => opts.only === undefined || c.name === opts.only)
      .map((c) => ({
        label: opts.prefix ? `${opts.prefix}.${c.name}` : c.name,
        apply: opts.prefix ? `${quoteIdent(kind, opts.prefix)}.${quoteIdent(kind, c.name)}` : quoteIdent(kind, c.name),
        type: 'property',
        detail: opts.showTable ? `${t.name} · ${c.type}` : c.type,
        boost,
      }))
  }

  switch (ctx.type) {
    case 'table': {
      const tables = cs.tables.map((t) => tableOption(kind, t, 10))
      const schemas = cs.schemas.filter((s) => s !== cs.schema).map((s): Completion => ({ label: s, apply: quoteIdent(kind, s), type: 'namespace', detail: 'schema', boost: 5 }))
      return { options: [...tables, ...schemas], keywords: false, missing }
    }
    case 'member': {
      // schema.<cursor>: that schema's tables (only the current one is known).
      if (!ctx.schema && sameName(kind, ctx.qualifier, cs.schema) && !refs.some((r) => r.alias && sameName(kind, ctx.qualifier, r.alias))) {
        return { options: cs.tables.map((t) => tableOption(kind, t, 10)), keywords: false, missing }
      }
      const t = ctx.schema ? resolveTable(kind, cs, { schema: ctx.schema, table: ctx.qualifier }) : resolveQualifier(kind, cs, refs, ctx.qualifier)
      return { options: t ? cols(t, 10) : [], keywords: false, missing }
    }
    case 'afterTable':
      return { options: [], keywords: true, missing }
    case 'column': {
      const scope = ctx.only ? [ctx.only] : refs
      const tables = scope.flatMap((r) => {
        const t = resolveTable(kind, cs, r)
        return t ? [{ ref: r, t }] : []
      })
      if (tables.length === 0) {
        // No table named yet (SELECT before FROM): tables, then keywords.
        return { options: cs.tables.map((t) => tableOption(kind, t, 1)), keywords: true, missing }
      }
      // A column name in several tables is offered qualified (a.id, b.id).
      const count = new Map<string, number>()
      for (const { t } of tables) for (const c of t.columns ?? []) count.set(c.name, (count.get(c.name) ?? 0) + 1)
      const several = tables.length > 1
      const options: Completion[] = []
      for (const { ref, t } of tables) {
        const prefix = ref.alias ?? ref.table
        for (const c of t.columns ?? []) {
          const dup = (count.get(c.name) ?? 0) > 1
          options.push(...cols(t, dup ? 9 : 10, { prefix: dup ? prefix : null, showTable: several && !dup, only: c.name }))
        }
        if (t.columns === null) missing.push(t.name)
        // The alias (or table) itself, to write "a." next.
        if (!ctx.only) options.push({ label: prefix, apply: quoteIdent(kind, prefix), type: 'variable', detail: ref.alias ? `alias of ${t.name}` : t.type, boost: 2 })
      }
      return { options, keywords: !ctx.only, missing }
    }
    default:
      return { options: cs.tables.map((t) => tableOption(kind, t, 1)), keywords: true, missing }
  }
}

// ---- CodeMirror source ---------------------------------------------------------

export interface CompletionDeps {
  kind: Kind
  /** The latest completion data. */
  schema(): CompletionSchema | null
  /** Asks for a table's columns (big schemas load them per table). */
  need(table: string): void
  /** Keyword completion of the dialect (lang-sql's keywordCompletionSource). */
  keywords: CompletionSource
}

/** The editor's completion source (see the module comment). */
export function completionSource(deps: CompletionDeps): CompletionSource {
  return async (ctx: CompletionContext): Promise<CompletionResult | null> => {
    const text = ctx.state.doc.toString()
    const toks = tokenize(deps.kind, text)
    // Inside a string, comment or quoted identifier: nothing to complete.
    if (toks.some((t) => (t.kind === 'string' || t.kind === 'comment' || t.kind === 'quoted') && t.from < ctx.pos && (ctx.pos < t.to || t.open))) return null
    const word = ctx.matchBefore(/[\w$\u0080-￿]*$/)
    const from = word ? word.from : ctx.pos
    const statement = statementAt(toks, ctx.pos)
    const before = statement.filter((t) => t.to <= from)
    const where = contextAt(before)
    // Without a typed character, open only where a name is expected.
    const typed = from < ctx.pos
    if (!typed && !ctx.explicit && where.type !== 'member' && where.type !== 'table') return null

    const built = buildOptions(deps.kind, deps.schema(), statement, where)
    for (const t of built.missing) deps.need(t)
    let options = built.options
    if (built.keywords && typed) {
      const kw = await deps.keywords(ctx)
      if (kw) options = [...options, ...kw.options.map((o) => ({ ...o, boost: (o.boost ?? 0) - 1 }))]
    }
    if (options.length === 0) return null
    return { from, options, validFor: /^[\w$\u0080-￿]*$/ }
  }
}
