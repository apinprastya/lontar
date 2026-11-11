import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import RowDetail, { detailText } from './RowDetail'

const columns = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'note', type: 'text', isPK: false, nullable: true },
  { name: 'doc', type: 'jsonb', isPK: false, nullable: true },
  { name: 'data', type: 'bytea', isPK: false, nullable: true },
  { name: 'empty', type: 'text', isPK: false, nullable: true },
]

describe('RowDetail', () => {
  afterEach(cleanup)

  it('shows every column with its full value', async () => {
    const onClose = vi.fn()
    const long = 'line one\nline two ' + 'x'.repeat(500)
    render(
      <RowDetail
        columns={columns}
        rowNumber={1301}
        onClose={onClose}
        row={[{ k: 'num', v: 7 }, { k: 'str', v: long, trunc: true }, { k: 'json', v: '{"a":1}' }, { k: 'bytes', v: '00ff', len: 2 }]}
      />,
    )
    const panel = screen.getByLabelText('Row detail')
    expect(panel).toHaveTextContent('Row 1,301')
    const values = panel.querySelectorAll('dd')
    expect(values[0]).toHaveTextContent('7')
    expect(values[0]).toHaveClass('num-value')
    expect(values[1].textContent).toContain(long)
    expect(values[1]).toHaveTextContent('(truncated)')
    expect(values[2].textContent).toBe('{\n  "a": 1\n}')
    expect(values[3].textContent).toBe('<binary 2 bytes>\n0x00ff')
    // A missing cell is NULL.
    expect(values[4]).toHaveTextContent('NULL')
    expect(values[4]).toHaveClass('null-value')
    expect(screen.getByTitle('Primary key')).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Close row detail' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('asks for a selection when no row is selected', () => {
    render(<RowDetail columns={columns} row={null} rowNumber={null} onClose={() => {}} />)
    expect(screen.getByText('Select a row to see its values.')).toBeInTheDocument()
    expect(screen.getByLabelText('Row detail')).toHaveTextContent('Row detail')
  })

  it('formats special values', () => {
    expect(detailText({ k: 'json', v: 'not json' })).toBe('not json')
    expect(detailText({ k: 'bytes', len: 0 })).toBe('<binary 0 bytes>')
    expect(detailText({ k: 'bytes', v: 'ab', len: 99999, trunc: true })).toBe('<binary 99,999 bytes>\n0xab…')
    expect(detailText({ k: 'str', v: '' })).toBe('')
    expect(detailText({ k: 'null' })).toBe('NULL')
  })
})
