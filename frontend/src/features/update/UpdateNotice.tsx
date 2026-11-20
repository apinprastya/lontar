import { useUpdate } from '../../stores/update'

/**
 * A small corner notice from the automatic update: the new version is
 * installed (restart to use it) or available but not installable here.
 * Opening it shows the update dialog, which has the restart and the
 * release page.
 */
export default function UpdateNotice() {
  const notice = useUpdate((s) => s.notice)
  const info = useUpdate((s) => s.info)
  const open = useUpdate((s) => s.open)
  if (!notice || !info || open) return null
  const { showNotice, dismissNotice } = useUpdate.getState()
  const installed = notice === 'installed'

  return (
    <div className="update-notice" role="status" aria-label="Software update">
      <span>
        Lontar <strong>{info.latest}</strong> {installed ? 'is installed. Restart to use it.' : 'is available.'}
      </span>
      <button type="button" className="primary" onClick={showNotice}>
        {installed ? 'Restart…' : 'Details'}
      </button>
      <button type="button" className="icon-button" aria-label="Dismiss update notice" onClick={dismissNotice}>
        ×
      </button>
    </div>
  )
}
