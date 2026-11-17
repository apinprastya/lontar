import { useState } from 'react'
import { copyText } from '../../lib/clipboard'
import { errorMessage } from '../../lib/errors'
import { useWorkspace } from '../../stores/workspace'
import DdlView from './DdlView'

export interface DdlSectionProps {
  /** The DDL (null while loading). */
  ddl: string | null
  error: string | null
  onRetry(): void
  /** Label of the copy button. */
  copyLabel?: string
}

/**
 * Read-only DDL with Copy and "Open in new query tab" (the structure
 * view's DDL section and the object tab).
 */
export default function DdlSection({ ddl, error, onRetry, copyLabel = 'Copy' }: DdlSectionProps) {
  const kind = useWorkspace((s) => s.kind)
  const newQueryTab = useWorkspace((s) => s.newQueryTab)
  const [copied, setCopied] = useState<string | null>(null)
  if (error)
    return (
      <div className="banner error" role="alert">
        <span className="selectable">{error}</span>
        <button type="button" onClick={onRetry}>
          Retry
        </button>
      </div>
    )
  if (ddl === null) return <p className="muted structure-empty">Loading DDL…</p>
  const copy = async () => {
    try {
      await copyText(ddl)
      setCopied('Copied')
    } catch (err) {
      setCopied(`Copy failed: ${errorMessage(err)}`)
    }
  }
  return (
    <div className="ddl-section">
      <div className="toolbar ddl-toolbar">
        <button type="button" onClick={() => void copy()}>
          {copyLabel}
        </button>
        <button type="button" onClick={() => newQueryTab(ddl)}>
          Open in new query tab
        </button>
        {copied && (
          <span className="muted" role="status">
            {copied}
          </span>
        )}
      </div>
      <DdlView text={ddl} kind={kind} />
    </div>
  )
}
