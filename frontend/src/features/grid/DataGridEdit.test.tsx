import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CompactSelection, type GridSelection } from '@glideapps/glide-data-grid'
import type { Cell, ResultColumn } from '../workspace/types'
import { displayRows, emptyPending, setCells, deleteRows, insertRow, type DisplayRow } from '../../stores/pending'

type KeyEv = { key: string; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean; cancel(): void; preventDefault(): void; stopPropagation(): void }
interface FakeProps {
  rows: number
  getCellContent(cell: [number, number]): { displayData: string; themeOverride?: Record<string, string> }
  onGridSelectionChange(sel: GridSelection): void
  onKeyDown(e: KeyEv): void
  onCellActivated?(cell: [number, number]): void
  onCellContextMenu?(cell: [number, number], e: { preventDefault(): void; bounds: { x: number; y: number }; localEventX: number; localEventY: number }): void
  onPaste: false | ((target: [number, number], values: string[][]) => boolean)
  onDelete(): boolean
  gridSelection?: GridSelection
  drawCell?(args: { ctx: unknown; rect: { x: number; y: number; width: number; height: number }; row: number; col: number }, draw: () => void): void
}
let lastProps: FakeProps | null = null
vi.mock('@glideapps/glide-data-grid', async (importOriginal) => {
  const real = await importOriginal<typeof import('@glideapps/glide-data-grid')>()
  const React = await import('react')
  return {
    ...real,
    DataEditor: React.forwardRef((p: FakeProps, _ref: unknown) => {
      lastProps = p
      return <div data-testid="fake-grid" />
    }),
  }
})

import DataGrid, { cellLook, countInserted, drawLock, isTypingKey, newRowCell, selectedCells, selectedRows, type GridEditing } from './DataGrid'

const columns: ResultColumn[] = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'name', type: 'text', isPK: false, nullable: true },
  { name: 'note', type: 'text', isPK: false, nullable: true },
]
const rows: Cell[][] = [
  [{ k: 'num', v: 1 }, { k: 'str', v: 'a' }, { k: 'str', v: 'long…', trunc: true }],
  [{ k: 'num', v: 2 }, { k: 'str', v: 'b' }, { k: 'null' }],
]
const sel = (cell: [number, number] | null, rowsSel: number[] = []): GridSelection => ({
  columns: CompactSelection.empty(),
  rows: rowsSel.reduce((s, r) => s.add(r), CompactSelection.empty()),
  current: cell ? { cell, range: { x: cell[0], y: cell[1], width: 1, height: 1 }, rangeStack: [] } : undefined,
})
const ev = (key: string, over: Partial<KeyEv> = {}): KeyEv => ({ key, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, cancel: vi.fn(), preventDefault: vi.fn(), stopPropagation: vi.fn(), ...over })

function states(): DisplayRow[] {
  let p = setCells(emptyPending, columns, rows, [{ ref: { type: 'row', index: 0 }, column: 'name', value: { kind: 'default' } }])
  p = deleteRows(p, columns, rows, [{ type: 'row', index: 1 }])
  p = insertRow(p).pending
  return displayRows(columns, rows, p)
}

function setup(over: Partial<GridEditing> = {}) {
  const st = states()
  const editing: GridEditing = {
    engine: 'postgres',
    rowStates: st,
    failed: null,
    editText: vi.fn((r: number, c: number) => String(st[r].cells[c].v ?? '')),
    onCommitCell: vi.fn(),
    onSetCells: vi.fn(),
    onDeleteRows: vi.fn(),
    onDuplicateRow: vi.fn(),
    onInsertRow: vi.fn(),
    onUndo: vi.fn(),
    onRedo: vi.fn(),
    onCommit: vi.fn(),
    onPaste: vi.fn(),
    ...over,
  }
  render(
    <DataGrid
      columns={columns}
      rows={st.map((r) => r.cells)}
      offset={0}
      sort={[]}
      columnWidths={{}}
      platform="linux"
      onHeaderClick={vi.fn()}
      onColumnResize={vi.fn()}
      onSelectRow={vi.fn()}
      onToggleDetail={vi.fn()}
      editing={editing}
    />,
  )
  return editing
}
const p = () => lastProps as FakeProps
const select = (cell: [number, number] | null, rs: number[] = []) => act(() => p().onGridSelectionChange(sel(cell, rs)))
const press = (key: string, over: Partial<KeyEv> = {}) => {
  const e = ev(key, over)
  act(() => p().onKeyDown(e))
  return e
}

describe('grid editing helpers', () => {
  it('maps row state to colours', () => {
    const st = states()
    expect(cellLook(undefined, 0, null)).toEqual({ strike: false, locked: false, isDefault: false })
    expect(cellLook(st[0], 1, null)).toMatchObject({ bg: expect.stringContaining('210'), isDefault: true, strike: false })
    expect(cellLook(st[0], 0, null).bg).toBeUndefined()
    expect(cellLook(st[0], 2, null).locked).toBe(true)
    expect(cellLook(st[1], 0, null)).toMatchObject({ strike: true, bg: expect.stringContaining('0.18') })
    expect(cellLook(st[2], 0, null).bg).toContain('63')
    expect(cellLook(st[0], 0, { key: st[0].key ?? '' }).bg).toContain('0.45')
    expect(cellLook(st[2], 0, { insertId: 1 }).bg).toContain('0.45')
    expect(cellLook(st[2], 0, { insertId: 2 }).bg).toContain('63')
  })

  it('reads selections and typing keys', () => {
    expect(selectedCells(sel([1, 0]), 3)).toEqual([[1, 0]])
    expect(selectedCells(sel(null, [1]), 2)).toEqual([[0, 1], [1, 1]])
    expect(selectedCells(sel(null), 2)).toEqual([])
    expect(selectedRows(sel([2, 1]))).toEqual([1])
    expect(selectedRows(sel(null, [0, 1]))).toEqual([0, 1])
    expect(selectedRows(sel(null))).toEqual([])
    expect(isTypingKey(ev('a'))).toBe(true)
    expect(isTypingKey(ev(' '))).toBe(false)
    expect(isTypingKey(ev('Enter'))).toBe(false)
    expect(isTypingKey(ev('a', { ctrlKey: true }))).toBe(false)
  })

  it('draws a lock', () => {
    const ctx = { save: vi.fn(), restore: vi.fn(), fillStyle: '', strokeStyle: '', lineWidth: 0, beginPath: vi.fn(), arc: vi.fn(), stroke: vi.fn(), fillRect: vi.fn() }
    drawLock(ctx, 10, 10, '#999')
    expect(ctx.arc).toHaveBeenCalled()
    expect(ctx.fillRect).toHaveBeenCalledWith(6.5, 9, 7, 5)
    expect(ctx.strokeStyle).toBe('#999')
  })
})

describe('DataGrid editing', () => {
  beforeEach(() => {
    lastProps = null
  })
  afterEach(cleanup)

  it('renders staged state and draws strikes and locks', () => {
    setup()
    expect(p().rows).toBe(3)
    expect(p().getCellContent([1, 0]).themeOverride).toMatchObject({ baseFontStyle: 'italic 12px' })
    expect(p().getCellContent([0, 0]).themeOverride).toBeUndefined()
    const ctx = { save: vi.fn(), restore: vi.fn(), beginPath: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), stroke: vi.fn(), arc: vi.fn(), fillRect: vi.fn(), strokeStyle: '', fillStyle: '', lineWidth: 0 }
    const draw = vi.fn()
    p().drawCell?.({ ctx, rect: { x: 0, y: 0, width: 100, height: 26 }, row: 1, col: 0 }, draw)
    expect(draw).toHaveBeenCalled()
    expect(ctx.moveTo).toHaveBeenCalledWith(4, 13)
    p().drawCell?.({ ctx, rect: { x: 0, y: 0, width: 100, height: 26 }, row: 0, col: 2 }, draw)
    expect(ctx.arc).toHaveBeenCalled()
    expect(p().onDelete()).toBe(false)
  })

  it('edits a cell on activation and commits', async () => {
    const user = userEvent.setup()
    const editing = setup()
    act(() => p().onCellActivated?.([2, 0])) // truncated: locked
    expect(screen.queryByTestId('cell-editor')).toBeNull()
    act(() => p().onCellActivated?.([1, 1])) // deleted row
    expect(screen.queryByTestId('cell-editor')).toBeNull()
    act(() => p().onCellActivated?.([1, 2]))
    const input = screen.getByRole('textbox', { name: 'Edit name' })
    await user.clear(input)
    await user.type(input, 'new{Enter}')
    expect(editing.onCommitCell).toHaveBeenCalledWith(2, 1, { kind: 'value', text: 'new' })
    expect(screen.queryByTestId('cell-editor')).toBeNull()
    // Escape cancels.
    act(() => p().onCellActivated?.([1, 2]))
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' })
    expect(screen.queryByTestId('cell-editor')).toBeNull()
  })

  it('starts editing with Enter or typing, forwarding keys that reach the grid', () => {
    const editing = setup()
    expect(press('Enter').cancel).not.toHaveBeenCalled() // no current cell
    select([1, 2])
    press('Enter')
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    press('Escape')
    expect(screen.queryByTestId('cell-editor')).toBeNull()
    const e = press('x')
    expect(e.cancel).toHaveBeenCalled()
    expect(screen.getByRole('textbox')).toHaveValue('x')
    press('y')
    expect(screen.getByRole('textbox')).toHaveValue('xy')
    expect(press('ArrowDown').cancel).not.toHaveBeenCalled()
    press('Enter')
    expect(editing.onCommitCell).toHaveBeenCalledWith(2, 1, { kind: 'value', text: 'xy' })
    // Typing on a locked cell does nothing.
    select([2, 0])
    expect(press('q').cancel).not.toHaveBeenCalled()
  })

  it('handles edit shortcuts', () => {
    const editing = setup()
    expect(press('Delete').cancel).not.toHaveBeenCalled() // nothing selected
    select([1, 0])
    press('Delete')
    expect(editing.onDeleteRows).toHaveBeenCalledWith([0])
    press('Backspace')
    expect(editing.onDeleteRows).toHaveBeenCalledTimes(2)
    press('N', { ctrlKey: true, shiftKey: true })
    expect(editing.onSetCells).toHaveBeenCalledWith([[1, 0]], { kind: 'null' })
    press('d', { ctrlKey: true })
    expect(editing.onDuplicateRow).toHaveBeenCalledWith(0)
    press('n', { ctrlKey: true })
    expect(editing.onInsertRow).toHaveBeenCalled()
    press('s', { ctrlKey: true })
    expect(editing.onCommit).toHaveBeenCalled()
    press('z', { ctrlKey: true })
    expect(editing.onUndo).toHaveBeenCalled()
    press('Z', { ctrlKey: true, shiftKey: true })
    expect(editing.onRedo).toHaveBeenCalled()
    select(null)
    press('d', { ctrlKey: true })
    expect(editing.onDuplicateRow).toHaveBeenCalledTimes(1)
  })

  it('offers a context menu', async () => {
    const user = userEvent.setup()
    const editing = setup()
    const open = (cell: [number, number]) =>
      act(() => p().onCellContextMenu?.(cell, { preventDefault: vi.fn(), bounds: { x: 10, y: 20 }, localEventX: 5, localEventY: 6 }))
    open([1, 0])
    const menu = screen.getByRole('menu')
    expect(menu).toHaveStyle({ left: '15px', top: '26px' })
    await user.click(screen.getByRole('menuitem', { name: /Set NULL/ }))
    expect(editing.onSetCells).toHaveBeenLastCalledWith([[1, 0]], { kind: 'null' })
    expect(screen.queryByRole('menu')).toBeNull()
    for (const [name, check] of [
      ['Set DEFAULT', () => expect(editing.onSetCells).toHaveBeenLastCalledWith([[1, 0]], { kind: 'default' })],
      ['Set empty string', () => expect(editing.onSetCells).toHaveBeenLastCalledWith([[1, 0]], { kind: 'value', text: '' })],
      ['Insert row', () => expect(editing.onInsertRow).toHaveBeenCalled()],
      ['Duplicate row', () => expect(editing.onDuplicateRow).toHaveBeenCalledWith(0)],
      ['Delete row', () => expect(editing.onDeleteRows).toHaveBeenCalledWith([0])],
    ] as const) {
      open([1, 0])
      await user.click(screen.getByRole('menuitem', { name: new RegExp(name) }))
      check()
    }
    // With a multi-row selection that includes the cell, the whole selection is used.
    select(null, [0, 2])
    open([1, 2])
    await user.click(screen.getByRole('menuitem', { name: /Delete row/ }))
    expect(editing.onDeleteRows).toHaveBeenLastCalledWith([0, 2])
    open([1, 2])
    await user.click(screen.getByRole('menuitem', { name: /Set NULL/ }))
    expect((editing.onSetCells as ReturnType<typeof vi.fn>).mock.lastCall?.[0]).toHaveLength(6)
    // Edit cell opens the editor; disabled for locked cells.
    open([2, 0])
    expect(screen.getByRole('menuitem', { name: /Edit cell/ })).toBeDisabled()
    fireEvent.mouseDown(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).toBeNull()
    open([1, 2])
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    open([1, 2])
    fireEvent.contextMenu(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).toBeNull()
    open([1, 2])
    await user.click(screen.getByRole('menuitem', { name: /Edit cell/ }))
    expect(screen.getByTestId('cell-editor')).toBeInTheDocument()
  })

  it('pastes TSV through the editing callbacks', () => {
    const editing = setup()
    const onPaste = p().onPaste
    expect(onPaste === false ? true : onPaste([1, 2], [['a', 'b']])).toBe(false)
    expect(editing.onPaste).toHaveBeenCalledWith(2, 1, [['a', 'b']])
  })

  it('stays read-only without editing', () => {
    render(
      <DataGrid columns={columns} rows={rows} offset={0} sort={[]} columnWidths={{}} onHeaderClick={vi.fn()} onColumnResize={vi.fn()} onSelectRow={vi.fn()} onToggleDetail={vi.fn()} />,
    )
    expect(p().onPaste).toBe(false)
    expect(p().onCellActivated).toBeUndefined()
    expect(p().drawCell).toBeUndefined()
    act(() => p().onGridSelectionChange(sel([1, 0])))
    expect(press('Delete').cancel).not.toHaveBeenCalled()
  })
})

describe('new rows', () => {
  afterEach(cleanup)

  it('counts staged inserts and finds the cell to land on', () => {
    const st = states()
    expect(countInserted(st)).toBe(1)
    expect(newRowCell(st)).toEqual([0, 2])
    // The last row is not an insert: nowhere to land.
    expect(newRowCell(st.slice(0, 2))).toBeNull()
    expect(newRowCell([])).toBeNull()
    // Locked columns are skipped; all locked falls back to the first.
    const locked = { ...st[2], locked: [true, false, false] }
    expect(newRowCell([...st.slice(0, 2), locked])).toEqual([1, 2])
    expect(newRowCell([{ ...locked, locked: [true, true, true] }])).toEqual([0, 0])
  })

  it('selects a newly staged row, but not on first render or undo', () => {
    const onSelectRow = vi.fn()
    const base = insertRow(deleteRows(emptyPending, columns, rows, [{ type: 'row', index: 1 }]), {}).pending
    const view = (pending: typeof base) => {
      const st = displayRows(columns, rows, pending)
      const editing = { rowStates: st, failed: null, engine: 'postgres' } as unknown as GridEditing
      return (
        <DataGrid
          columns={columns}
          rows={st.map((r) => r.cells)}
          offset={0}
          sort={[]}
          columnWidths={{}}
          platform="linux"
          onHeaderClick={vi.fn()}
          onColumnResize={vi.fn()}
          onSelectRow={onSelectRow}
          onToggleDetail={vi.fn()}
          editing={editing}
        />
      )
    }
    const { rerender } = render(view(base))
    // Opening a tab that already has staged inserts does not jump.
    expect(onSelectRow).not.toHaveBeenCalled()
    expect(p().gridSelection?.current).toBeUndefined()

    const more = insertRow(base).pending
    rerender(view(more))
    expect(onSelectRow).toHaveBeenLastCalledWith(3)
    expect(p().gridSelection?.current?.cell).toEqual([0, 3])

    // Fewer inserts (undo) leaves the selection alone.
    onSelectRow.mockClear()
    rerender(view(base))
    expect(onSelectRow).not.toHaveBeenCalled()
  })
})
