import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CompactSelection, type GridSelection } from '@glideapps/glide-data-grid'
import type { Cell, ResultColumn } from '../workspace/types'

type KeyEv = { key: string; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean; altKey: boolean; cancel(): void; preventDefault(): void; stopPropagation(): void }
interface FakeProps {
  columns: { id: string; icon: string }[]
  getCellContent(cell: [number, number]): { contentAlign?: string }
  onGridSelectionChange(sel: GridSelection): void
  onKeyDown(e: KeyEv): void
  onCellClicked?(cell: [number, number], e: { localEventX: number; bounds: { width: number } }): void
  onCellContextMenu?(cell: [number, number], e: { preventDefault(): void; bounds: { x: number; y: number }; localEventX: number; localEventY: number }): void
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

import DataGrid, { LINK_HIT_WIDTH, columnIcon, drawLink, headerIcons, type GridLinks } from './DataGrid'

const columns: ResultColumn[] = [
  { name: 'id', type: 'integer', isPK: true, nullable: false },
  { name: 'parent_id', type: 'integer', isPK: false, nullable: true },
  { name: 'name', type: 'text', isPK: false, nullable: true },
]
const rows: Cell[][] = [
  [{ k: 'num', v: 1 }, { k: 'num', v: 7 }, { k: 'str', v: 'a' }],
  [{ k: 'num', v: 2 }, { k: 'null' }, { k: 'str', v: 'b' }],
]
const ev = (key: string, over: Partial<KeyEv> = {}): KeyEv => ({ key, ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, cancel: vi.fn(), preventDefault: vi.fn(), stopPropagation: vi.fn(), ...over })
const sel = (cell: [number, number]): GridSelection => ({
  columns: CompactSelection.empty(),
  rows: CompactSelection.empty(),
  current: { cell, range: { x: cell[0], y: cell[1], width: 1, height: 1 }, rangeStack: [] },
})

function setup(fkColumns = [true, true, false]) {
  const links: GridLinks = {
    fkColumns,
    canFollow: vi.fn((row: number, col: number) => fkColumns[col] && rows[row][col].k !== 'null'),
    onFollow: vi.fn(),
  }
  render(
    <DataGrid
      columns={columns}
      rows={rows}
      offset={0}
      sort={[]}
      columnWidths={{}}
      platform="linux"
      onHeaderClick={vi.fn()}
      onColumnResize={vi.fn()}
      onSelectRow={vi.fn()}
      onToggleDetail={vi.fn()}
      links={links}
    />,
  )
  return { links, p: () => lastProps as FakeProps }
}

describe('DataGrid foreign key links', () => {
  afterEach(cleanup)

  it('marks foreign key headers with link icons', () => {
    const { p } = setup()
    expect(p().columns.map((c) => c.icon)).toEqual(['pkfk', 'fk', columnIcon(columns[2])])
    expect(columnIcon(columns[1], true)).toBe('fk')
    expect(headerIcons.fk()).toContain('<svg')
    expect(headerIcons.pkfk()).toContain('<svg')
    // Followable key values are left-aligned, clear of the arrow.
    expect(p().getCellContent([1, 0]).contentAlign).toBe('left')
    expect(p().getCellContent([1, 1]).contentAlign).toBeUndefined()
    expect(p().getCellContent([0, 0]).contentAlign).toBe('left')
  })

  it('draws the arrow only on followable cells and follows clicks on it', () => {
    const { p, links } = setup()
    const ctx = { save: vi.fn(), restore: vi.fn(), fillText: vi.fn(), fillStyle: '', font: '', textAlign: '', textBaseline: '' }
    const rect = { x: 0, y: 0, width: 100, height: 26 }
    const draw = vi.fn()
    p().drawCell?.({ ctx, rect, row: 0, col: 1 }, draw)
    expect(draw).toHaveBeenCalled()
    expect(ctx.fillText).toHaveBeenCalledWith('→', 100 - LINK_HIT_WIDTH / 2, 13)
    ctx.fillText.mockClear()
    p().drawCell?.({ ctx, rect, row: 1, col: 1 }, draw) // NULL: no affordance
    p().drawCell?.({ ctx, rect, row: 0, col: 2 }, draw) // not a key
    expect(ctx.fillText).not.toHaveBeenCalled()

    p().onCellClicked?.([1, 0], { localEventX: 10, bounds: { width: 100 } })
    expect(links.onFollow).not.toHaveBeenCalled()
    p().onCellClicked?.([1, 1], { localEventX: 95, bounds: { width: 100 } })
    expect(links.onFollow).not.toHaveBeenCalled()
    p().onCellClicked?.([1, 0], { localEventX: 95, bounds: { width: 100 } })
    expect(links.onFollow).toHaveBeenCalledWith(0, 1)
  })

  it('follows with Mod+O on the selected cell', () => {
    const { p, links } = setup()
    const e0 = ev('o', { ctrlKey: true })
    act(() => p().onKeyDown(e0)) // nothing selected: consumed, no-op
    expect(e0.cancel).toHaveBeenCalled()
    expect(links.onFollow).not.toHaveBeenCalled()
    act(() => p().onGridSelectionChange(sel([1, 0])))
    act(() => p().onKeyDown(ev('o', { ctrlKey: true })))
    expect(links.onFollow).toHaveBeenCalledWith(0, 1)
    act(() => p().onKeyDown(ev('o'))) // without Mod
    expect(links.onFollow).toHaveBeenCalledTimes(1)
  })

  it('offers "Open referenced row" in the context menu of a read-only grid', () => {
    const { p, links } = setup()
    const ctxEv = (x: number) => ({ preventDefault: vi.fn(), bounds: { x, y: 0 }, localEventX: 1, localEventY: 1 })
    act(() => p().onCellContextMenu?.([1, 1], ctxEv(0)))
    const item = screen.getByRole('menuitem', { name: /Open referenced row/ })
    expect(item).toBeDisabled() // NULL key
    expect(screen.queryByRole('menuitem', { name: /Set NULL/ })).toBeNull()
    fireEvent.mouseDown(document.querySelector('.context-menu-backdrop') as Element)
    act(() => p().onCellContextMenu?.([1, 0], ctxEv(0)))
    fireEvent.click(screen.getByRole('menuitem', { name: /Open referenced row/ }))
    expect(links.onFollow).toHaveBeenCalledWith(0, 1)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('draws the link arrow with the given colour', () => {
    const ctx = { save: vi.fn(), restore: vi.fn(), fillText: vi.fn(), fillStyle: '', font: '', textAlign: '', textBaseline: '' }
    drawLink(ctx, { x: 10, y: 20, width: 50, height: 20 }, 'red')
    expect(ctx.fillStyle).toBe('red')
    expect(ctx.fillText).toHaveBeenCalledWith('→', 60 - LINK_HIT_WIDTH / 2, 30)
  })
})
