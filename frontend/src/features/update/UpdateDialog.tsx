import { useEffect, useId, useRef } from 'react'
import { useUpdate } from '../../stores/update'

/** Formats a byte count as MB with one decimal. */
export function megabytes(n: number): string {
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/** Formats an RFC 3339 date as a local calendar date ("" when absent). */
function releaseDate(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
}

/**
 * The update dialog: shows the result of a check for updates (run only when
 * the user asks), downloads and installs a verified release, and restarts
 * into it.
 */
export default function UpdateDialog() {
  const uid = useId()
  const { phase, info, progress, error } = useUpdate()
  const { setOpen, install, cancel, restart, openReleasePage, check } = useUpdate.getState()
  const focusRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)

  // The phase's main action, or the dialog itself (so Escape works).
  useEffect(() => {
    ;(focusRef.current ?? dialogRef.current)?.focus()
  }, [phase, info])

  const close = () => setOpen(false)
  const pct = progress && progress.total > 0 ? Math.min(100, Math.round((progress.done / progress.total) * 100)) : null
  const date = info ? releaseDate(info.publishedAt) : ''

  return (
    <div className="dialog-backdrop">
      <div
        ref={dialogRef}
        tabIndex={-1}
        className="dialog update-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${uid}-title`}
        aria-busy={phase === 'checking' || phase === 'installing'}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            close()
          }
        }}
      >
        <h2 id={`${uid}-title`}>Software update</h2>

        {phase === 'checking' && <p className="update-status">Checking for updates…</p>}

        {info && !info.available && phase === 'checked' && (
          <p className="update-status">
            You are up to date. Lontar <strong>{info.current}</strong> is the latest version
            {info.latest !== info.current ? ` (latest release: ${info.latest})` : ''}.
          </p>
        )}

        {info?.available && (
          <>
            <p className="update-status">
              Lontar <strong>{info.latest}</strong> is available. You have {info.current}.
              {date && <span className="hint"> Released {date}.</span>}
            </p>
            {info.notes.trim() && (
              <pre className="update-notes" aria-label="Release notes">
                {info.notes.trim()}
              </pre>
            )}
            {!info.canInstall && info.reason && <p className="hint">{info.reason}</p>}
          </>
        )}

        {phase === 'installing' && (
          <div className="update-progress">
            <progress aria-label="Download progress" max={100} value={pct ?? undefined} />
            <span className="hint">
              {progress ? `${megabytes(progress.done)}${progress.total > 0 ? ` of ${megabytes(progress.total)}` : ''}` : 'Starting download…'}
            </span>
          </div>
        )}

        {phase === 'installed' && info && (
          <p className="update-status">
            Lontar <strong>{info.latest}</strong> is installed. Restart to start using it. Restarting closes open connections; staged changes
            that were not committed and unsaved query text are lost.
          </p>
        )}

        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          {info?.available && phase !== 'installed' && (
            <button type="button" onClick={() => void openReleasePage()}>
              Release page
            </button>
          )}
          <span className="spacer" />
          {phase === 'idle' && (
            <button ref={focusRef} type="button" onClick={() => void check()}>
              Try again
            </button>
          )}
          {phase === 'installing' ? (
            <button ref={focusRef} type="button" onClick={() => void cancel()}>
              Cancel download
            </button>
          ) : (
            <button type="button" onClick={close}>
              {phase === 'installed' ? 'Later' : 'Close'}
            </button>
          )}
          {phase === 'checked' && info?.canInstall && (
            <button ref={focusRef} type="button" className="primary" onClick={() => void install()}>
              Download and install
            </button>
          )}
          {phase === 'installed' && (
            <button ref={focusRef} type="button" className="primary" onClick={() => void restart()}>
              Restart now
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
