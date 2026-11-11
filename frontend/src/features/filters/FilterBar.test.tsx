import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { newFilterRow, type FilterRow } from '../../stores/workspace'
import FilterBar from './FilterBar'

const columns = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'name', type: 'text', isPK: false, nullable: true },
]

function Harness({ initial, onApply, onClear, startRaw = false }: { initial: FilterRow[]; onApply: (rows: FilterRow[], raw: string, rawMode: boolean) => void; onClear?: () => void; startRaw?: boolean }) {
  const [rows, setRows] = useState(initial)
  const [rawMode, setRawMode] = useState(startRaw)
  const [raw, setRaw] = useState('')
  return (
    <FilterBar
      columns={columns}
      rows={rows}
      rawMode={rawMode}
      rawWhere={raw}
      active={!!onClear}
      onChange={setRows}
      onRawModeChange={setRawMode}
      onRawWhereChange={setRaw}
      onApply={() => onApply(rows, raw, rawMode)}
      onClear={onClear ?? (() => {})}
    />
  )
}

describe('FilterBar', () => {
  afterEach(cleanup)

  it('edits rows and applies with Enter', async () => {
    const onApply = vi.fn()
    const user = userEvent.setup()
    render(<Harness initial={[newFilterRow('id')]} onApply={onApply} />)
    await user.selectOptions(screen.getByLabelText('Column 1'), 'name')
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'contains')
    await user.type(screen.getByLabelText('Value 1'), 'abc{Enter}')
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(onApply.mock.calls[0][0][0]).toMatchObject({ column: 'name', op: 'contains', value: 'abc' })
  })

  it('hides the value for isNull and shows two inputs for between', async () => {
    const user = userEvent.setup()
    render(<Harness initial={[newFilterRow('id')]} onApply={vi.fn()} />)
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'isNull')
    expect(screen.queryByLabelText('Value 1')).toBeNull()
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'isNotNull')
    expect(screen.queryByLabelText('Value 1')).toBeNull()
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'between')
    expect(screen.getByLabelText('Value 1')).toHaveAttribute('placeholder', 'from')
    await user.type(screen.getByLabelText('Second value 1'), '9')
    expect(screen.getByLabelText('Second value 1')).toHaveValue('9')
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'in')
    expect(screen.getByLabelText('Value 1')).toHaveAttribute('placeholder', 'a, b, c')
  })

  it('adds and removes rows', async () => {
    const user = userEvent.setup()
    render(<Harness initial={[newFilterRow('id')]} onApply={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(screen.getAllByTestId('filter-row')).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Remove filter 1' }))
    await user.click(screen.getByRole('button', { name: 'Remove filter 1' }))
    expect(screen.queryAllByTestId('filter-row')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(screen.getAllByTestId('filter-row')).toHaveLength(1)
  })

  it('switches to raw SQL mode', async () => {
    const onApply = vi.fn()
    const user = userEvent.setup()
    render(<Harness initial={[newFilterRow('id')]} onApply={onApply} />)
    await user.click(screen.getByRole('checkbox', { name: /Raw SQL/ }))
    expect(screen.queryByLabelText('Column 1')).toBeNull()
    await user.type(screen.getByLabelText('Raw SQL WHERE condition'), "name = 'x'")
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    expect(onApply).toHaveBeenCalledWith(expect.any(Array), "name = 'x'", true)
  })

  it('clears only when a filter is active', async () => {
    const onClear = vi.fn()
    const { unmount } = render(<Harness initial={[]} onApply={vi.fn()} />)
    expect(screen.getByRole('button', { name: 'Clear' })).toBeDisabled()
    unmount()
    render(<Harness initial={[]} onApply={vi.fn()} onClear={onClear} />)
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }))
    expect(onClear).toHaveBeenCalled()
  })

  it('ignores other keys', () => {
    const onApply = vi.fn()
    render(<Harness initial={[newFilterRow('id')]} onApply={onApply} startRaw />)
    fireEvent.keyDown(screen.getByLabelText('Raw SQL WHERE condition'), { key: 'a' })
    expect(onApply).not.toHaveBeenCalled()
  })
})
