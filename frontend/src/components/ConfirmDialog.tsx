import { useEffect, useId, useRef } from 'react'

export interface ConfirmDialogProps {
  title: string
  message: string
  confirmLabel?: string
  danger?: boolean
  onConfirm(): void
  onCancel(): void
}

/** Small modal confirmation (Escape cancels, Enter confirms). */
export default function ConfirmDialog({ title, message, confirmLabel = 'OK', danger, onConfirm, onCancel }: ConfirmDialogProps) {
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
