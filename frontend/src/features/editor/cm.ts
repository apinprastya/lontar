/**
 * CodeMirror 6 building blocks of the SQL editor. Everything here works on
 * EditorState (no DOM), so it is unit-testable in jsdom.
 */
import { HighlightStyle } from '@codemirror/language'
import { MariaSQL, MySQL, PostgreSQL, SQLite, StandardSQL, type SQLDialect } from '@codemirror/lang-sql'
import { Prec, StateEffect, StateField, type EditorState, type Extension } from '@codemirror/state'
import { Decoration, EditorView, keymap, type DecorationSet } from '@codemirror/view'
import { tags as t } from '@lezer/highlight'
import type { Kind } from '../connections/types'

/** The lang-sql dialect for a connection kind. */
export function sqlDialect(kind: Kind): SQLDialect {
  switch (kind) {
    case 'postgres':
      return PostgreSQL
    case 'mysql':
      return MySQL
    case 'mariadb':
      return MariaSQL
    case 'sqlite':
      return SQLite
  }
  return StandardSQL
}

export interface Range {
  from: number
  to: number
}

function clampRange(state: EditorState, r: Range): Range | null {
  const len = state.doc.length
  const from = Math.max(0, Math.min(r.from, len))
  const to = Math.max(from, Math.min(r.to, len))
  return to > from ? { from, to } : null
}

/** Sets (or clears) the highlighted current statement. */
export const setStatement = StateEffect.define<Range | null>()

const statementMark = Decoration.mark({ class: 'cm-current-statement' })

/** Highlights the statement under the cursor. */
export const statementField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(deco, tr) {
    let next = deco.map(tr.changes)
    for (const e of tr.effects) {
      if (e.is(setStatement)) {
        const r = e.value && clampRange(tr.state, e.value)
        next = r ? Decoration.set([statementMark.range(r.from, r.to)]) : Decoration.none
      }
    }
    return next
  },
  provide: (f) => EditorView.decorations.from(f),
})

/** Marks an error position (cleared by the next edit). */
export const setErrorMark = StateEffect.define<number | null>()

const errorMark = Decoration.mark({ class: 'cm-sql-error' })

/** The word (or character) at pos that an error points to. */
export function errorRange(state: EditorState, pos: number): Range | null {
  const len = state.doc.length
  if (len === 0) return null
  const p = Math.max(0, Math.min(pos, len - 1))
  const word = state.wordAt(p)
  if (word && word.from <= p && word.to > p) return { from: word.from, to: word.to }
  return { from: p, to: p + 1 }
}

export const errorField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(deco, tr) {
    let next = tr.docChanged ? Decoration.none : deco
    for (const e of tr.effects) {
      if (e.is(setErrorMark)) {
        const r = e.value === null ? null : errorRange(tr.state, e.value)
        next = r ? Decoration.set([errorMark.range(r.from, r.to)]) : Decoration.none
      }
    }
    return next
  },
  provide: (f) => EditorView.decorations.from(f),
})

export interface RunHandlers {
  run(mode: 'current' | 'all'): void
  /** Cancels a running query; false when nothing runs (Escape then does its usual job). */
  cancel(): boolean
}

/** The run keys, ahead of CodeMirror's own bindings (Mod-Enter inserts a line by default). */
export function runKeymap(h: () => RunHandlers): Extension {
  return Prec.highest(
    keymap.of([
      { key: 'Mod-Enter', run: () => (h().run('current'), true), preventDefault: true },
      { key: 'Mod-Shift-Enter', run: () => (h().run('all'), true), preventDefault: true },
      { key: 'Escape', run: () => h().cancel() },
    ]),
  )
}

/** Editor chrome from the app's CSS variables (follows light/dark). */
export const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'var(--bg)',
    color: 'var(--text)',
    fontSize: 'var(--editor-font-size, 13px)',
  },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.5' },
  '.cm-content': { caretColor: 'var(--text)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text)' },
  '&.cm-focused': { outline: 'none' },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, ::selection': {
    backgroundColor: 'var(--bg-selected) !important',
  },
  '.cm-gutters': { backgroundColor: 'var(--bg-sidebar)', color: 'var(--muted)', borderRight: '1px solid var(--border)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--bg-hover)', color: 'var(--text)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--bg-hover) 45%, transparent)' },
  '.cm-matchingBracket': { backgroundColor: 'var(--bg-selected)', outline: '1px solid var(--accent)' },
  '.cm-current-statement': { backgroundColor: 'var(--stmt-bg)' },
  '.cm-sql-error': { textDecoration: 'underline wavy var(--danger)', backgroundColor: 'var(--error-bg)' },
  '.cm-searchMatch': { backgroundColor: 'var(--search-bg)', outline: '1px solid var(--accent)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'var(--bg-selected)' },
  '.cm-panels': { backgroundColor: 'var(--bg-sidebar)', color: 'var(--text)', borderColor: 'var(--border)' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid var(--border)' },
  '.cm-search': {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: '4px 6px',
    padding: '6px 28px 6px 8px',
    position: 'relative',
    fontSize: '12px',
  },
  '.cm-search br': { display: 'none' },
  '.cm-search input.cm-textfield': {
    width: '200px',
    margin: '0',
    padding: '3px 8px',
    fontSize: '12px',
    backgroundColor: 'var(--bg-input)',
    color: 'var(--text)',
    border: '1px solid var(--border)',
  },
  '.cm-search input.cm-textfield:focus': { borderColor: 'var(--accent)', outline: 'none' },
  '.cm-search button.cm-button': {
    margin: '0',
    padding: '3px 9px',
    fontSize: '12px',
    backgroundImage: 'none',
    backgroundColor: 'var(--bg-raised)',
    color: 'var(--text)',
    border: '1px solid var(--border)',
  },
  '.cm-search button.cm-button:hover': { backgroundColor: 'var(--bg-hover)' },
  '.cm-search label': { display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: '12px', color: 'var(--muted)', whiteSpace: 'nowrap' },
  '.cm-search input[type=checkbox]': { margin: '0', width: 'auto' },
  '.cm-search button[name=close]': {
    position: 'absolute',
    top: '4px',
    right: '6px',
    border: 'none',
    background: 'transparent',
    padding: '0 6px',
    fontSize: '16px',
    lineHeight: '1.2',
    color: 'var(--muted)',
  },
  '.cm-search button[name=close]:hover': { backgroundColor: 'var(--bg-hover)', color: 'var(--text)' },
  '.cm-tooltip': { backgroundColor: 'var(--bg-dialog)', border: '1px solid var(--border)', color: 'var(--text)' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'var(--accent)', color: 'var(--accent-text)' },
  '.cm-completionDetail': { color: 'var(--muted)', fontStyle: 'normal', marginLeft: '1em' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected] .cm-completionDetail': { color: 'inherit' },
})

/** Syntax colours (CSS variables defined for light and dark in App.css). */
export const sqlHighlightStyle = HighlightStyle.define([
  { tag: t.keyword, color: 'var(--syn-keyword)', fontWeight: '600' },
  { tag: [t.string, t.special(t.string)], color: 'var(--syn-string)' },
  { tag: [t.number, t.bool, t.null], color: 'var(--syn-number)' },
  { tag: [t.lineComment, t.blockComment], color: 'var(--muted)', fontStyle: 'italic' },
  { tag: [t.typeName, t.standard(t.name)], color: 'var(--syn-type)' },
  { tag: [t.special(t.name), t.propertyName], color: 'var(--syn-ident)' },
  { tag: [t.operator, t.punctuation, t.bracket], color: 'var(--muted)' },
])
