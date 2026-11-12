import { useEffect, useId, useRef } from 'react'
import type { HostKeyInfo } from '../../lib/hostkey'

export interface HostKeyDialogProps {
  info: HostKeyInfo
  busy?: boolean
  onAccept(): void
  onCancel(): void
}

/**
 * Asks whether to trust an unknown SSH host key. Accepting records the key
 * in Lontar's own known_hosts (never ~/.ssh/known_hosts). A changed key is
 * shown as a warning without an Accept button: it must be fixed by hand.
 */
export default function HostKeyDialog({ info, busy, onAccept, onCancel }: HostKeyDialogProps) {
  const uid = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const changed = info.kind === 'hostKeyChanged'

  useEffect(() => {
    cancelRef.current?.focus()
  }, [])

  return (
    <div className="dialog-backdrop">
      <div
        className={`dialog hostkey-dialog${changed ? ' hostkey-changed' : ''}`}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${uid}-title`}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
      >
        <h2 id={`${uid}-title`}>{changed ? 'SSH host key has changed!' : 'Unknown SSH host'}</h2>
        {changed ? (
          <p className="error">
            The server <strong>{info.host}</strong> presented a different key than the one recorded for it. Someone could be
            intercepting the connection. Lontar will not connect. If the change is expected, remove the old entry by hand from:{' '}
            {(info.known ?? []).join(', ')}
          </p>
        ) : (
          <p>
            The authenticity of <strong>{info.host}</strong> can't be established. Check the fingerprint with the server's
            administrator before trusting it.
          </p>
        )}
        <dl className="hostkey-facts">
          <dt>Key type</dt>
          <dd className="mono">{info.keyType}</dd>
          <dt>Fingerprint</dt>
          <dd className="mono" data-testid="hostkey-fingerprint">
            {info.fingerprintSHA256}
          </dd>
        </dl>
        {!changed && <p className="hint">Trusted keys are saved in Lontar's own known_hosts file; ~/.ssh/known_hosts is never modified.</p>}
        <div className="dialog-actions">
          <span className="spacer" />
          <button ref={cancelRef} type="button" onClick={onCancel} disabled={busy}>
            {changed ? 'Close' : 'Cancel'}
          </button>
          {!changed && (
            <button type="button" className="primary" onClick={onAccept} disabled={busy}>
              {busy ? 'Saving…' : 'Trust and connect'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
