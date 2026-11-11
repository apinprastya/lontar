import type { ExecResult } from './types'
import { firstLine, formatDuration, statementSummary } from './format'

const ICONS = { ok: '✓', error: '✗', cancelled: '⊘', skipped: '–' } as const

export interface MessagesProps {
  result: ExecResult
  /** Selects the statement (or its error position) in the editor. */
  onShow(index: number): void
}

/** The Messages tab: every statement of the run with its outcome. */
export default function Messages({ result, onShow }: MessagesProps) {
  return (
    <div className="messages" role="list" aria-label="Messages">
      {result.notices.map((n, i) => (
        <div key={`n${i}`} className="banner warning messages-notice" role="alert">
          {n}
        </div>
      ))}
      {result.statements.length === 0 && <p className="muted messages-empty">No statements to run.</p>}
      {result.statements.map((st) => (
        <button
          key={st.index}
          type="button"
          role="listitem"
          className={`message message-${st.status}`}
          onClick={() => onShow(st.index)}
          title={st.error?.position !== null && st.error ? 'Show the error position in the editor' : 'Select this statement in the editor'}
        >
          <span className="message-icon" aria-label={st.status}>
            {ICONS[st.status]}
          </span>
          <span className="message-body">
            <span className="message-sql mono">{firstLine(st.sql)}</span>
            <span className="message-meta">
              <span>{statementSummary(st)}</span>
              {st.status !== 'skipped' && <span className="muted">{formatDuration(st.durationMs)}</span>}
            </span>
            {st.error && st.status === 'error' && (
              <span className="message-error selectable">
                {st.error.code && <span className="message-code">[{st.error.code}]</span>} {st.error.message}
                {st.error.detail && <span className="message-detail">Detail: {st.error.detail}</span>}
                {st.error.hint && <span className="message-detail">Hint: {st.error.hint}</span>}
              </span>
            )}
          </span>
        </button>
      ))}
      <p className="muted messages-total">
        {result.cancelled ? 'Cancelled after ' : 'Total time '}
        {formatDuration(result.durationMs)}
      </p>
    </div>
  )
}
