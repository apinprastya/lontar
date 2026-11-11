import { sql } from '@codemirror/lang-sql'
import { syntaxHighlighting } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { EditorView, lineNumbers } from '@codemirror/view'
import { useEffect, useRef } from 'react'
import type { Kind } from '../connections/types'
import { editorTheme, sqlDialect, sqlHighlightStyle } from '../editor/cm'

/** Read-only CodeMirror view of DDL with the connection's SQL dialect. */
export default function DdlView({ text, kind }: { text: string; kind: Kind }) {
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const view = new EditorView({
      state: EditorState.create({
        doc: text,
        extensions: [
          lineNumbers(),
          sql({ dialect: sqlDialect(kind), upperCaseKeywords: true }),
          syntaxHighlighting(sqlHighlightStyle),
          editorTheme,
          EditorState.readOnly.of(true),
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ 'aria-label': 'DDL', 'aria-readonly': 'true' }),
        ],
      }),
      parent: host.current as HTMLDivElement,
    })
    return () => view.destroy()
  }, [text, kind])

  return <div className="ddl-view sql-editor" ref={host} data-testid="ddl-view" />
}
