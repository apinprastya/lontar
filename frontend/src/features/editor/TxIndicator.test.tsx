import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import TxIndicator from './TxIndicator'

describe('TxIndicator', () => {
  it('shows open, guessed and failed transactions only', () => {
    const { rerender, container } = render(<TxIndicator tx={null} />)
    expect(container).toBeEmptyDOMElement()
    rerender(<TxIndicator tx={{ state: 'idle', source: 'server' }} />)
    expect(container).toBeEmptyDOMElement()
    rerender(<TxIndicator tx={{ state: 'active', source: 'server' }} />)
    expect(screen.getByTestId('tx-indicator')).toHaveTextContent('Transaction open')
    expect(screen.getByTestId('tx-indicator')).not.toHaveTextContent('guess')
    rerender(<TxIndicator tx={{ state: 'active', source: 'heuristic' }} />)
    expect(screen.getByTestId('tx-indicator')).toHaveTextContent('Transaction open (guess)')
    expect(screen.getByTestId('tx-indicator').title).toMatch(/BEGIN\/COMMIT/)
    rerender(<TxIndicator tx={{ state: 'failed', source: 'server' }} />)
    expect(screen.getByTestId('tx-indicator')).toHaveTextContent('Transaction failed')
    expect(screen.getByTestId('tx-indicator')).toHaveClass('tx-failed')
  })
})
