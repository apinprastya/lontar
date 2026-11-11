import { useEffect, useId, useRef, useState } from 'react'
import { copyText } from '../../lib/clipboard'
import type { PreviewStatement, SafeMode } from './types'

/** Preview of the generated SQL (display only; Go runs bound parameters). */
export function PreviewDialog({
  statements,
  safeMode,
  committing,
  onCommit,
  onClose,
}: {
  statements: PreviewStatement[]
  safeMode: SafeMode
  committing: boolean
  onCommit(): void
  onClose(): void
}) {
  const uid = useId()
  const [copied, setCopied] = useState(false)
  const commitRef = useRef<HTMLButtonElement>(null)
  useEffect(() => commitRef.current?.focus(), [])
  const sql = statements.map((s) => s.sql).join('\n')
  return (
    <div className="dialog-backdrop">
      <div
        className="dialog preview-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${uid}-t`}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onClose()
          }
        }}
      >
        <h2 id={`${uid}-t`}>Review changes</h2>
        <p className="muted">
          {statements.length} statement{statements.length === 1 ? '' : 's'}, run in one transaction. This is a readable rendering: the values are sent to the database as bound
          parameters.
          {safeMode === 'confirm' && ' This connection asks for confirmation before writing.'}
        </p>
        <pre className="preview-sql selectable" data-testid="preview-sql">
          {sql}
        </pre>
        <div className="dialog-actions">
          <button
            type="button"
            onClick={() => {
              void copyText(sql).then(() => setCopied(true))
            }}
          >
            {copied ? 'Copied' : 'Copy SQL'}
          </button>
          <span className="spacer" />
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button ref={commitRef} type="button" className="primary" disabled={committing} onClick={onCommit}>
            {committing ? 'Committing…' : 'Commit'}
          </button>
        </div>
      </div>
    </div>
  )
}

/** Commit / Discard / Cancel before an action that would lose staged edits. */
export function UnsavedDialog({ count, busy, onChoose }: { count: number; busy: boolean; onChoose(c: 'commit' | 'discard' | 'cancel'): void }) {
  const uid = useId()
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <div className="dialog-backdrop">
      <div
        className="dialog confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${uid}-t`}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onChoose('cancel')
          }
        }}
      >
        <h2 id={`${uid}-t`}>Unsaved changes</h2>
        <p>
          You have {count} uncommitted change{count === 1 ? '' : 's'}. Commit them first, or discard them?
        </p>
        <div className="dialog-actions">
          <button type="button" className="danger" disabled={busy} onClick={() => onChoose('discard')}>
            Discard
          </button>
          <span className="spacer" />
          <button type="button" disabled={busy} onClick={() => onChoose('cancel')}>
            Cancel
          </button>
          <button ref={ref} type="button" className="primary" disabled={busy} onClick={() => onChoose('commit')}>
            {busy ? 'Committing…' : 'Commit'}
          </button>
        </div>
      </div>
    </div>
  )
}

/** Confirm safe mode: lists the write statements of an SQL editor run. */
export function ConfirmRunDialog({ statements, onConfirm, onCancel }: { statements: string[]; onConfirm(): void; onCancel(): void }) {
  const uid = useId()
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <div className="dialog-backdrop">
      <div
        className="dialog preview-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${uid}-t`}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
      >
        <h2 id={`${uid}-t`}>Run statements that write?</h2>
        <p className="muted">
          This connection is in confirm safe mode. The run contains {statements.length} statement{statements.length === 1 ? '' : 's'} that can change data or settings:
        </p>
        <pre className="preview-sql selectable" data-testid="write-statements">
          {statements.map((s) => s.trim() + ';').join('\n\n')}
        </pre>
        <div className="dialog-actions">
          <span className="spacer" />
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
          <button ref={ref} type="button" className="danger" onClick={onConfirm}>
            Run
          </button>
        </div>
      </div>
    </div>
  )
}
