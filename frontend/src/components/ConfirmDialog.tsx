import { useEffect, useId, useRef } from 'react'

export interface ConfirmDialogProps {
  title: string
  message: string
  confirmLabel?: string
  danger?: boolean
  /** An optional third choice, shown on the left (e.g. "Don't save"). */
  altLabel?: string
  onAlt?(): void
  onConfirm(): void
  onCancel(): void
}

/** Small modal confirmation (Escape cancels, Enter confirms). */
export default function ConfirmDialog({ title, message, confirmLabel = 'OK', danger, altLabel, onAlt, onConfirm, onCancel }: ConfirmDialogProps) {
  const uid = useId()
  const confirmRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    confirmRef.current?.focus()
  }, [])

  return (
    <div className="dialog-backdrop">
      <div
        className="dialog confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${uid}-title`}
        aria-describedby={`${uid}-msg`}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
      >
        <h2 id={`${uid}-title`}>{title}</h2>
        <p id={`${uid}-msg`}>{message}</p>
        <div className="dialog-actions">
          {altLabel && onAlt && (
            <button type="button" className="danger" onClick={onAlt}>
              {altLabel}
            </button>
          )}
          <span className="spacer" />
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
          <button ref={confirmRef} type="button" className={danger ? 'danger' : 'primary'} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
