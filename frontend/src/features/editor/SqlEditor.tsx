import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { sql } from '@codemirror/lang-sql'
import { bracketMatching, indentOnInput, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, openSearchPanel, search, searchKeymap } from '@codemirror/search'
import { Compartment, EditorSelection, EditorState } from '@codemirror/state'
import { EditorView, drawSelection, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
import { useEffect, useRef } from 'react'
import type { Kind } from '../connections/types'
import type { EditorRequest, Range } from '../../stores/queryTabs'
import {
  editorTheme,
  errorField,
  lazyColumnsSource,
  runKeymap,
  setErrorMark,
  setStatement,
  sqlConfig,
  sqlHighlightStyle,
  statementField,
  type RunHandlers,
} from './cm'
import type { CompletionSchema } from './types'

export interface SqlEditorProps {
  value: string
  /** Cursor position when the editor mounts (e.g. switching back to the tab). */
  initialCursor?: number
  kind: Kind
  completion: CompletionSchema | null
  /** The statement under the cursor (highlighted). */
  current: Range | null
  /** One-shot request (select, error, insert, search), applied once per seq. */
  request: EditorRequest | null
  onChange(sql: string): void
  onSelection(cursor: number, selection: Range | null): void
  onRun(mode: 'current' | 'all'): void
  /** Cancels a running query; returns false when nothing runs. */
  onCancel(): boolean
  onNeedColumns(table: string): void
}

function languageExtension(kind: Kind, cs: CompletionSchema | null, getCs: () => CompletionSchema | null, need: (t: string) => void) {
  const lang = sql(sqlConfig(kind, cs))
  return [lang, lang.language.data.of({ autocomplete: lazyColumnsSource(getCs, need) })]
}

/** Applies an editor request to a view. */
export function applyRequest(view: EditorView, req: EditorRequest) {
  const len = view.state.doc.length
  const clamp = (n: number) => Math.max(0, Math.min(n, len))
  switch (req.type) {
    case 'select':
      view.dispatch({ selection: EditorSelection.range(clamp(req.from), clamp(req.to)), scrollIntoView: true })
      break
    case 'error':
      view.dispatch({ selection: EditorSelection.cursor(clamp(req.pos)), effects: setErrorMark.of(clamp(req.pos)), scrollIntoView: true })
      break
    case 'insert':
      view.dispatch(view.state.replaceSelection(req.text))
      break
    case 'search':
      openSearchPanel(view)
      return
  }
  view.focus()
}

/** CodeMirror 6 SQL editor with schema-aware completion. */
export default function SqlEditor(props: SqlEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const latest = useRef(props)
  latest.current = props
  const language = useRef(new Compartment())
  // A request made before this editor mounted (e.g. on a previous visit
  // of the tab) was already applied.
  const appliedSeq = useRef<number | null>(props.request?.seq ?? null)

  useEffect(() => {
    const p = latest.current
    const handlers = (): RunHandlers => ({ run: (mode) => latest.current.onRun(mode), cancel: () => latest.current.onCancel() })
    const getCs = () => latest.current.completion
    const need = (t: string) => latest.current.onNeedColumns(t)
    const state = EditorState.create({
      doc: p.value,
      selection: EditorSelection.cursor(Math.min(p.initialCursor ?? p.value.length, p.value.length)),
      extensions: [
        runKeymap(handlers),
        lineNumbers(),
        highlightActiveLineGutter(),
        history(),
        drawSelection(),
        indentOnInput(),
        bracketMatching(),
        closeBrackets(),
        autocompletion({ activateOnTyping: true }),
        highlightActiveLine(),
        highlightSelectionMatches(),
        search({ top: true }),
        keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...searchKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
        language.current.of(languageExtension(p.kind, p.completion, getCs, need)),
        statementField,
        errorField,
        editorTheme,
        syntaxHighlighting(sqlHighlightStyle),
        EditorView.lineWrapping,
        EditorView.contentAttributes.of({ 'aria-label': 'SQL editor' }),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) latest.current.onChange(u.state.doc.toString())
          if (u.docChanged || u.selectionSet) {
            const sel = u.state.selection.main
            latest.current.onSelection(sel.head, sel.empty ? null : { from: sel.from, to: sel.to })
          }
        }),
      ],
    })
    const v = new EditorView({ state, parent: host.current as HTMLDivElement })
    view.current = v
    v.focus()
    return () => {
      v.destroy()
      view.current = null
    }
  }, [])

  // Outside changes of the text (e.g. a restored draft).
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== props.value) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: props.value } })
    }
  }, [props.value])

  useEffect(() => {
    const getCs = () => latest.current.completion
    const need = (t: string) => latest.current.onNeedColumns(t)
    view.current?.dispatch({ effects: language.current.reconfigure(languageExtension(props.kind, props.completion, getCs, need)) })
  }, [props.kind, props.completion])

  useEffect(() => {
    view.current?.dispatch({ effects: setStatement.of(props.current) })
  }, [props.current])

  useEffect(() => {
    const v = view.current
    const req = props.request
    if (!v || !req || appliedSeq.current === req.seq) return
    appliedSeq.current = req.seq
    applyRequest(v, req)
  }, [props.request])

  return <div className="sql-editor" ref={host} data-testid="sql-editor" />
}
