import type { TxInfo } from './types'

/** "Transaction open" badge for an editor whose connection is in a transaction. */
export default function TxIndicator({ tx }: { tx: TxInfo | null }) {
  if (!tx || (tx.state !== 'active' && tx.state !== 'failed')) return null
  const guess = tx.source === 'heuristic'
  const failed = tx.state === 'failed'
  const title = failed
    ? 'The transaction failed; run ROLLBACK to continue.'
    : guess
      ? 'A transaction appears to be open (tracked from BEGIN/COMMIT statements). COMMIT or ROLLBACK to end it; closing the tab rolls it back.'
      : 'A transaction is open on this tab’s connection. COMMIT or ROLLBACK to end it; closing the tab rolls it back.'
  return (
    <span className={`tx-indicator${failed ? ' tx-failed' : ''}`} role="status" title={title} data-testid="tx-indicator">
      <span className="tx-dot" aria-hidden="true" />
      {failed ? 'Transaction failed' : 'Transaction open'}
      {guess && !failed && <span className="muted"> (guess)</span>}
    </span>
  )
}
