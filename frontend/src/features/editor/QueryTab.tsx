import { useEffect } from 'react'
import { Group, Panel, Separator } from 'react-resizable-panels'
import { shortcutLabel } from '../../lib/mod'
import type { QueryTabState } from '../../stores/queryTabs'
import { useWorkspace } from '../../stores/workspace'
import { editorApi, type EditorApi } from './api'
import HistoryPanel from './HistoryPanel'
import ResultsPanel from './ResultsPanel'
import SqlEditor from './SqlEditor'
import TxIndicator from './TxIndicator'
import { MAX_ROWS_OPTIONS } from './types'

/** Debounce of the current-statement highlight (a backend call). */
export const HIGHLIGHT_DELAY = 120

export interface QueryTabProps {
  tab: QueryTabState
  platform?: string
  /** History backend; tests inject a fake. */
  api?: Pick<EditorApi, 'historyList' | 'historyDelete' | 'historyClear'>
}

/** An SQL editor tab: toolbar, editor over results, optional history. */
export default function QueryTab({ tab, platform, api = editorApi }: QueryTabProps) {
  const s = useWorkspace((st) => st)
  const { kind, completion, historyOpen, connectionId, schema } = s

  useEffect(() => {
    void s.loadCompletion()
  }, [schema, completion])

  useEffect(() => {
    const t = setTimeout(() => void s.updateCurrentStatement(tab.id), HIGHLIGHT_DELAY)
    return () => clearTimeout(t)
  }, [tab.id, tab.sql, tab.cursor])

  const run = shortcutLabel(platform, 'Enter')
  const runAll = shortcutLabel(platform, 'Enter', { shift: true })

  const editorAndResults = (
    <Group orientation="vertical" className="query-split">
      <Panel id="editor" defaultSize="45" minSize={80}>
        <SqlEditor
          value={tab.sql}
          initialCursor={tab.cursor}
          kind={kind}
          completion={completion}
          current={tab.current}
          request={tab.editorRequest}
          onChange={(sql) => s.setSql(tab.id, sql)}
          onSelection={(cursor, sel) => s.setEditorSelection(tab.id, cursor, sel)}
          onRun={(mode) => void s.runQuery(tab.id, mode)}
          onCancel={() => {
            if (!tab.running) return false
            s.cancelQuery(tab.id)
            return true
          }}
          onNeedColumns={(table) => void s.loadCompletionColumns(table)}
        />
      </Panel>
      <Separator className="resize-handle resize-handle-h" />
      <Panel id="results" minSize={80}>
        <ResultsPanel tab={tab} platform={platform} />
      </Panel>
    </Group>
  )

  return (
    <div className="query-tab" role="tabpanel" aria-label={tab.title}>
      <div className="toolbar query-toolbar">
        <button type="button" disabled={tab.running} onClick={() => void s.runQuery(tab.id, 'current')} title={`Run the selection or the statement under the cursor (${run})`}>
          {tab.selection ? 'Run selection' : 'Run'}
        </button>
        <button type="button" disabled={tab.running} onClick={() => void s.runQuery(tab.id, 'all')} title={`Run every statement (${runAll})`}>
          Run all
        </button>
        <button type="button" disabled={!tab.running} onClick={() => s.cancelQuery(tab.id)} title="Stop the running query (Esc)">
          Stop
        </button>
        <label className="toolbar-check" title="Run the remaining statements after one fails">
          <input type="checkbox" checked={tab.continueOnError} onChange={(e) => s.setContinueOnError(tab.id, e.target.checked)} />
          Continue on error
        </label>
        <label className="toolbar-select">
          Max rows
          <select value={tab.maxRows} onChange={(e) => s.setMaxRows(tab.id, Number(e.target.value))} aria-label="Max rows">
            {MAX_ROWS_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n.toLocaleString('en-US')}
              </option>
            ))}
          </select>
        </label>
        <TxIndicator tx={tab.tx} />
        {tab.running && <span className="muted">Running…</span>}
        <span className="spacer" />
        <button type="button" aria-pressed={historyOpen} onClick={() => s.toggleHistory()}>
          History
        </button>
      </div>
      <div className="query-body">
        {historyOpen ? (
          <Group orientation="horizontal">
            <Panel id="query-main" minSize={300}>
              {editorAndResults}
            </Panel>
            <Separator className="resize-handle" />
            <Panel id="history" defaultSize={300} minSize={200}>
              <HistoryPanel
                api={api}
                connectionId={connectionId}
                refreshKey={tab.result}
                onInsert={(sql) => s.insertIntoEditor(sql)}
                onOpen={(sql) => s.newQueryTab(sql)}
                onClose={() => s.toggleHistory(false)}
              />
            </Panel>
          </Group>
        ) : (
          editorAndResults
        )}
      </div>
    </div>
  )
}
