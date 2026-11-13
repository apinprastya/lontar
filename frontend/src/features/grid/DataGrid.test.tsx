import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GridSelection } from '@glideapps/glide-data-grid'
import type { Cell, ResultColumn } from '../workspace/types'

const copyText = vi.fn<(t: string) => Promise<void>>()
vi.mock('../../lib/clipboard', () => ({ copyText: (t: string) => copyText(t) }))

// Glide draws on a canvas, which jsdom lacks. The fake exposes the props the
// wrapper passes so the mapping logic can be tested.
interface FakeProps {
  columns: { id: string; title: string; width: number; icon: string }[]
  rows: number
  getCellContent(cell: [number, number]): { displayData: string; contentAlign?: string; themeOverride?: unknown }
  onHeaderClicked(col: number, e: { shiftKey: boolean }): void
  onColumnResize(col: { id?: string; title: string }, width: number): void
  onGridSelectionChange(sel: GridSelection): void
  drawHeader(args: unknown, draw: () => void): void
  rowMarkers: { startIndex: number }
  experimental?: { paddingBottom?: number; paddingRight?: number }
  onKeyDown(e: { key: string; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean; cancel(): void; preventDefault(): void; stopPropagation(): void }): void
}
let lastProps: FakeProps | null = null
vi.mock('@glideapps/glide-data-grid', async (importOriginal) => {
  const real = await importOriginal<typeof import('@glideapps/glide-data-grid')>()
  return {
    ...real,
    DataEditor: (await import('react')).forwardRef((p: FakeProps, _ref: unknown) => {
      lastProps = p
      return (
        <div data-testid="fake-grid" tabIndex={0}>
          {p.columns.map((c, i) => (
            <button key={c.id} type="button" onClick={(e) => p.onHeaderClicked(i, { shiftKey: e.shiftKey })}>
              {c.title}
            </button>
          ))}
          <table>
            <tbody>
              {Array.from({ length: p.rows }, (_, r) => (
                <tr key={r}>
                  {p.columns.map((_, c) => {
                    const cell = p.getCellContent([c, r])
                    return (
                      <td key={c} data-align={cell.contentAlign ?? 'left'} data-dim={cell.themeOverride ? 'yes' : 'no'}>
                        {cell.displayData}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
    }),
  }
})

import { CompactSelection } from '@glideapps/glide-data-grid'
import DataGrid, { SCROLLBAR_GUTTER, columnIcon, drawTypeLabel, gridTheme, selectedRowOf, toSelectionSpec } from './DataGrid'

const columns: ResultColumn[] = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'name', type: 'text', isPK: false, nullable: true },
  { name: 'data', type: 'bytea', isPK: false, nullable: true },
]
const rows: Cell[][] = [
  [{ k: 'num', v: 1 }, { k: 'str', v: 'alpha' }, { k: 'bytes', v: '00', len: 1 }],
  [{ k: 'num', v: 2 }, { k: 'null' }, { k: 'null' }],
  [{ k: 'num', v: 3 }, { k: 'str', v: '' }, { k: 'bytes', v: 'ff00', len: 2048 }],
]

const sel = (over: Partial<GridSelection>): GridSelection => ({ columns: CompactSelection.empty(), rows: CompactSelection.empty(), ...over })

function setup(over: Partial<Parameters<typeof DataGrid>[0]> = {}) {
  const props = {
    columns,
    rows,
    offset: 300,
    sort: [],
    columnWidths: { name: 150 },
    platform: 'linux',
    onHeaderClick: vi.fn(),
    onColumnResize: vi.fn(),
    onSelectRow: vi.fn(),
    onToggleDetail: vi.fn(),
    ...over,
  }
  render(<DataGrid {...props} />)
  return props
}

describe('DataGrid wrapper', () => {
  beforeEach(() => {
    copyText.mockReset()
    copyText.mockResolvedValue()
    lastProps = null
  })
  afterEach(cleanup)

  it('maps cells: NULL dim, empty string empty, numbers right, bytes labelled', () => {
    setup()
    const cells = screen.getAllByRole('row').map((r) => Array.from(r.querySelectorAll('td')))
    expect(cells[0][0]).toHaveTextContent('1')
    expect(cells[0][0]).toHaveAttribute('data-align', 'right')
    expect(cells[0][1]).toHaveAttribute('data-align', 'left')
    expect(cells[1][1]).toHaveTextContent('NULL')
    expect(cells[1][1]).toHaveAttribute('data-dim', 'yes')
    expect(cells[2][1].textContent).toBe('')
    expect(cells[2][1]).toHaveAttribute('data-dim', 'no')
    expect(cells[2][2]).toHaveTextContent('<binary 2,048 bytes>')
    expect(cells[2][2]).toHaveAttribute('data-dim', 'yes')
    expect(lastProps?.rowMarkers.startIndex).toBe(301)
    expect(lastProps?.columns.map((c) => [c.title, c.width, c.icon])).toEqual([
      ['id', 120, 'pk'],
      ['name', 150, 'headerString'],
      ['data', 120, 'headerImage'],
    ])
    expect(lastProps?.getCellContent([0, 99]).displayData).toBe('NULL')
  })

  it('reports header clicks with shift for multi-sort', () => {
    const p = setup({ sort: [{ column: 'id', desc: true }] })
    fireEvent.click(screen.getByRole('button', { name: 'id ▼' }))
    expect(p.onHeaderClick).toHaveBeenLastCalledWith('id', false)
    fireEvent.click(screen.getByRole('button', { name: 'name' }), { shiftKey: true })
    expect(p.onHeaderClick).toHaveBeenLastCalledWith('name', true)
    lastProps?.onHeaderClicked(99, { shiftKey: false })
    expect(p.onHeaderClick).toHaveBeenCalledTimes(2)
  })

  it('leaves scroll room so the last row and column clear the scrollbars', () => {
    setup()
    expect(SCROLLBAR_GUTTER).toBeGreaterThanOrEqual(10)
    expect(lastProps?.experimental).toEqual({ paddingBottom: SCROLLBAR_GUTTER, paddingRight: SCROLLBAR_GUTTER })
  })

  it('reports column resizes', () => {
    const p = setup()
    lastProps?.onColumnResize({ id: 'name', title: 'name' }, 200)
    expect(p.onColumnResize).toHaveBeenCalledWith('name', 200)
    lastProps?.onColumnResize({ title: 'data' }, 99)
    expect(p.onColumnResize).toHaveBeenCalledWith('data', 99)
  })

  it('copies the selection as TSV with Mod+C and toggles detail with Space', () => {
    const p = setup()
    act(() => lastProps?.onGridSelectionChange(sel({ current: { cell: [0, 1], range: { x: 0, y: 0, width: 2, height: 2 }, rangeStack: [] } })))
    expect(p.onSelectRow).toHaveBeenLastCalledWith(1)
    const cancel = vi.fn()
    const press = (key: string, mods: { ctrlKey?: boolean; metaKey?: boolean } = {}) =>
      lastProps?.onKeyDown({ key, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, ...mods, cancel, preventDefault: () => {}, stopPropagation: () => {} })
    press('c', { ctrlKey: true })
    expect(copyText).toHaveBeenCalledWith('1\talpha\n2\t')
    expect(cancel).toHaveBeenCalledTimes(1)
    press('c', { metaKey: true }) // not the Linux modifier
    expect(copyText).toHaveBeenCalledTimes(1)
    press(' ')
    expect(p.onToggleDetail).toHaveBeenCalledTimes(1)
    press(' ', { ctrlKey: true })
    expect(p.onToggleDetail).toHaveBeenCalledTimes(1)
    expect(cancel).toHaveBeenCalledTimes(2)
  })

  it('does not copy an empty selection', () => {
    setup()
    const cancel = vi.fn()
    lastProps?.onKeyDown({ key: 'c', ctrlKey: true, metaKey: false, shiftKey: false, altKey: false, cancel, preventDefault: () => {}, stopPropagation: () => {} })
    expect(copyText).not.toHaveBeenCalled()
    expect(cancel).not.toHaveBeenCalled()
  })

  it('draws the type label in the header', () => {
    setup()
    const draw = vi.fn()
    const ctx = { measureText: () => ({ width: 10 }), fillText: vi.fn(), save: vi.fn(), restore: vi.fn(), fillStyle: '', font: '', textAlign: '', textBaseline: '' }
    lastProps?.drawHeader({ ctx, columnIndex: 1, rect: { x: 0, y: 0, width: 200, height: 30 }, column: { title: 'name' } }, draw)
    expect(draw).toHaveBeenCalled()
    expect(ctx.fillText).toHaveBeenCalledWith('text', 192, 15)
    lastProps?.drawHeader({ ctx, columnIndex: 9, rect: { x: 0, y: 0, width: 200, height: 30 }, column: { title: 'x' } }, draw)
    expect(ctx.fillText).toHaveBeenCalledTimes(1)
  })
})


describe('grid helpers', () => {
  it('converts selections', () => {
    expect(toSelectionSpec(sel({ rows: CompactSelection.fromSingleSelection([1, 3]) }))).toEqual({ rows: [1, 2] })
    expect(toSelectionSpec(sel({ columns: CompactSelection.fromSingleSelection(2) }))).toEqual({ columns: [2] })
    expect(toSelectionSpec(sel({}))).toEqual({})
    expect(selectedRowOf(sel({ rows: CompactSelection.fromSingleSelection(4) }))).toBe(4)
    expect(selectedRowOf(sel({}))).toBeNull()
  })

  it('picks header icons by type', () => {
    const c = (type: string): ResultColumn => ({ name: 'x', type, isPK: false, nullable: true })
    expect(columnIcon(c('numeric(10,2)'))).toBe('headerNumber')
    expect(columnIcon(c('boolean'))).toBe('headerBoolean')
    expect(columnIcon(c('timestamp'))).toBe('headerDate')
    expect(columnIcon(c('jsonb'))).toBe('headerCode')
    expect(columnIcon(c('varchar(3)'))).toBe('headerString')
  })

  it('skips the type label when it does not fit or is empty', () => {
    const ctx = { measureText: () => ({ width: 100 }), fillText: vi.fn(), save: vi.fn(), restore: vi.fn(), fillStyle: '', font: '', textAlign: '', textBaseline: '' }
    expect(drawTypeLabel(ctx, { x: 0, y: 0, width: 120, height: 30 }, 'name', 'text', '#999')).toBe(false)
    expect(drawTypeLabel(ctx, { x: 0, y: 0, width: 999, height: 30 }, 'name', '', '#999')).toBe(false)
  })

  it('builds a theme with fallbacks', () => {
    document.documentElement.style.setProperty('--accent', '#123456')
    const t = gridTheme()
    expect(t.accentColor).toBe('#123456')
    expect(t.textDark).toBe('#e6e6ea')
  })
})
