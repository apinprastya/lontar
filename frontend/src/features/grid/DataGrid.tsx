import { useResolvedTheme } from '../../stores/settings'
import {
  CompactSelection,
  DataEditor,
  GridCellKind,
  GridColumnIcon,
  type DataEditorRef,
  type DrawCellCallback,
  type DrawHeaderCallback,
  type GridCell,
  type GridColumn,
  type GridSelection,
  type Item,
  type Theme,
} from '@glideapps/glide-data-grid'
import '@glideapps/glide-data-grid/dist/index.css'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { flushSync } from 'react-dom'
import { copyText } from '../../lib/clipboard'
import { isModPressed, matchesShortcut, shortcutLabel, type ModifierEvent } from '../../lib/mod'
import type { Kind } from '../connections/types'
import { CellEditor, type EditorHandle } from '../edits/editors'
import type { EditValue } from '../edits/types'
import type { DisplayRow } from '../../stores/pending'
import type { Cell, ResultColumn, SortSpec } from '../workspace/types'
import { cellDisplay, defaultColumnWidth, headerTitle, isNull, isNumeric, selectionToTSV, type SelectionSpec } from './cells'

export interface DataGridProps {
  columns: ResultColumn[]
  rows: Cell[][]
  /** Row number of the first row (page offset), for the row markers. */
  offset: number
  sort: SortSpec[]
  columnWidths: Record<string, number>
  platform?: string
  onHeaderClick(column: string, additive: boolean): void
  onColumnResize(column: string, width: number): void
  onSelectRow(row: number | null): void
  onToggleDetail(): void
  /** Editing (table tabs of editable tables); absent = read-only grid. */
  editing?: GridEditing
  /** Foreign key navigation (table tabs); absent = no links. */
  links?: GridLinks
  /** "Export selection…" in the context menu; absent = not offered. */
  onExportSelection?(sel: SelectionSpec): void
}

/** Foreign key affordances of a table grid. Rows/cols are grid indexes. */
export interface GridLinks {
  /** Per column: part of a foreign key (link icon in the header). */
  fkColumns: boolean[]
  /** Whether the cell holds a reference that can be followed (non-NULL key). */
  canFollow(row: number, col: number): boolean
  /** Opens the referenced row. */
  onFollow(row: number, col: number): void
}

/** Width of the clickable "→" at the right edge of a foreign key cell. */
export const LINK_HIT_WIDTH = 18

/** Callbacks and state of an editable grid. Rows/cols are grid indexes. */
export interface GridEditing {
  engine: Kind
  /** One per grid row (fetched rows, then staged inserts). */
  rowStates: DisplayRow[]
  /** The failed change of the last commit. */
  failed: { key?: string; insertId?: number } | null
  /** Text the editor starts with. */
  editText(row: number, col: number): string
  onCommitCell(row: number, col: number, value: EditValue): void
  onSetCells(cells: Item[], value: EditValue): void
  onDeleteRows(rows: number[]): void
  onDuplicateRow(row: number): void
  onInsertRow(): void
  onUndo(): void
  onRedo(): void
  onCommit(): void
  /** TSV pasted at a cell (top-left). */
  onPaste(row: number, col: number, values: readonly (readonly string[])[]): void
}

/** Visual state of one grid cell. */
export interface CellLook {
  bg?: string
  strike: boolean
  locked: boolean
  isDefault: boolean
}

/** Maps a row's staged state to colours (pure; CSS variables with fallbacks). */
export function cellLook(d: DisplayRow | undefined, col: number, failed: GridEditing['failed']): CellLook {
  if (!d) return { strike: false, locked: false, isDefault: false }
  const isFailed =
    failed !== null && ((failed.key !== undefined && d.key === failed.key) || (failed.insertId !== undefined && d.ref.type === 'insert' && d.ref.id === failed.insertId))
  let bg: string | undefined
  if (isFailed) bg = cssVar('--edit-failed', 'rgba(248,81,73,0.45)')
  else if (d.state === 'deleted') bg = cssVar('--edit-deleted', 'rgba(248,81,73,0.18)')
  else if (d.state === 'inserted') bg = cssVar('--edit-inserted', 'rgba(63,185,80,0.18)')
  else if (d.modified[col]) bg = cssVar('--edit-modified', 'rgba(210,153,34,0.28)')
  return { bg, strike: d.state === 'deleted', locked: Boolean(d.locked[col]), isDefault: Boolean(d.defaults[col]) }
}

/** The cells of a selection (current range, else whole selected rows). */
export function selectedCells(sel: GridSelection, columns: number): Item[] {
  const out: Item[] = []
  if (sel.rows.length > 0) {
    for (const r of sel.rows.toArray()) for (let c = 0; c < columns; c++) out.push([c, r])
    return out
  }
  if (sel.current) {
    const { x, y, width, height } = sel.current.range
    for (let r = y; r < y + height; r++) for (let c = x; c < x + width; c++) out.push([c, r])
  }
  return out
}

/** The rows a selection touches. */
export function selectedRows(sel: GridSelection): number[] {
  if (sel.rows.length > 0) return sel.rows.toArray()
  if (sel.current) {
    const { y, height } = sel.current.range
    return Array.from({ length: height }, (_, i) => y + i)
  }
  return []
}

/** Number of staged inserts among the grid rows. */
export function countInserted(rows: readonly DisplayRow[]): number {
  let n = 0
  for (const r of rows) if (r.state === 'inserted') n++
  return n
}

/**
 * The cell to land on after a row is staged: the last grid row (inserts are
 * appended) at its first editable column. Null when the last row is not a
 * staged insert.
 */
export function newRowCell(rows: readonly DisplayRow[]): Item | null {
  const row = rows.length - 1
  const d = rows[row]
  if (!d || d.state !== 'inserted') return null
  const col = d.locked.findIndex((l) => !l)
  return [col === -1 ? 0 : col, row]
}

/** A printable key that starts editing with that character. */
export function isTypingKey(e: ModifierEvent): boolean {
  return e.key.length === 1 && e.key !== ' ' && !e.ctrlKey && !e.metaKey && !e.altKey
}

type EditingCell = { col: number; row: number; typed?: string; x: number; y: number; width: number }
type MenuState = { col: number; row: number; x: number; y: number }

const emptySelection: GridSelection = { columns: CompactSelection.empty(), rows: CompactSelection.empty() }

function cssVar(name: string, fallback: string): string {
  const v = typeof document !== 'undefined' ? getComputedStyle(document.documentElement).getPropertyValue(name).trim() : ''
  return v || fallback
}

/** Glide theme derived from the app's CSS variables (light/dark aware). */
export function gridTheme(): Partial<Theme> {
  const text = cssVar('--text', '#e6e6ea')
  const muted = cssVar('--muted', '#9a9aa6')
  const bg = cssVar('--bg', '#18191e')
  const border = cssVar('--border', '#33343c')
  const accent = cssVar('--accent', '#4c82f0')
  return {
    accentColor: accent,
    accentLight: cssVar('--bg-selected', 'rgba(76,130,240,0.2)'),
    textDark: text,
    textMedium: muted,
    textLight: muted,
    textHeader: text,
    textHeaderSelected: cssVar('--accent-text', '#ffffff'),
    bgCell: bg,
    bgCellMedium: cssVar('--bg-sidebar', '#1f2026'),
    bgHeader: cssVar('--bg-sidebar', '#1f2026'),
    bgHeaderHasFocus: cssVar('--bg-hover', '#2a2b33'),
    bgHeaderHovered: cssVar('--bg-hover', '#2a2b33'),
    bgIconHeader: muted,
    fgIconHeader: bg,
    borderColor: border,
    horizontalBorderColor: border,
    fontFamily: cssVar('--font-mono', 'monospace'),
    baseFontStyle: '12px',
    headerFontStyle: '600 12px',
    markerFontStyle: '11px',
  }
}

/** Header icon by column type (foreign keys get a link). */
export function columnIcon(column: ResultColumn, fk = false): string {
  if (fk) return column.isPK ? 'pkfk' : 'fk'
  if (column.isPK) return 'pk'
  const t = column.type.toLowerCase()
  if (/int|num|dec|float|double|real|serial|money/.test(t)) return GridColumnIcon.HeaderNumber
  if (/bool/.test(t)) return GridColumnIcon.HeaderBoolean
  if (/date|time/.test(t)) return GridColumnIcon.HeaderDate
  if (/json/.test(t)) return GridColumnIcon.HeaderCode
  if (/bin|blob|bytea/.test(t)) return GridColumnIcon.HeaderImage
  return GridColumnIcon.HeaderString
}

/** Key icon for primary-key columns, link icons for foreign keys. */
export const headerIcons = {
  pk: () =>
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="7" cy="10" r="3.5" fill="none" stroke="#d29922" stroke-width="2"/><path d="M10.5 10h7m-2 0v3m-2.5-3v2" stroke="#d29922" stroke-width="2" fill="none" stroke-linecap="round"/></svg>',
  fk: () =>
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><g fill="none" stroke="#4c82f0" stroke-width="2" stroke-linecap="round"><path d="M8.5 11.5l3-3"/><path d="M10 6l1.5-1.5a3 3 0 0 1 4.2 4.2L14 10.2"/><path d="M10 14l-1.5 1.5a3 3 0 0 1-4.2-4.2L6 9.8"/></g></svg>',
  pkfk: () =>
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="5.5" cy="7" r="3" fill="none" stroke="#d29922" stroke-width="2"/><path d="M8.5 7h5" stroke="#d29922" stroke-width="2" stroke-linecap="round"/><g fill="none" stroke="#4c82f0" stroke-width="2" stroke-linecap="round"><path d="M11 16l3-3"/><path d="M13 12l1-1a2.1 2.1 0 0 1 3 3l-1 1"/><path d="M12 17l-1 1a2.1 2.1 0 0 1-3-3l1-1"/></g></svg>',
}

/** Draws the "→" of a followable foreign key cell at the right edge. */
export function drawLink(ctx: { save(): void; restore(): void; fillStyle: unknown; font: string; textAlign: string; textBaseline: string; fillText(t: string, x: number, y: number): void }, rect: { x: number; y: number; width: number; height: number }, color: string) {
  ctx.save()
  ctx.fillStyle = color
  ctx.font = 'bold 13px sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText('→', rect.x + rect.width - LINK_HIT_WIDTH / 2, rect.y + rect.height / 2)
  ctx.restore()
}

/** Glide cell for a value. */
export function toGridCell(cell: Cell | undefined, mutedColor: string): GridCell {
  const text = cellDisplay(cell)
  const base = { kind: GridCellKind.Text as const, data: text, displayData: text, allowOverlay: false, readonly: true }
  if (isNull(cell)) {
    return { ...base, themeOverride: { textDark: mutedColor, baseFontStyle: 'italic 12px' } }
  }
  if (cell?.k === 'bytes') return { ...base, themeOverride: { textDark: mutedColor } }
  if (isNumeric(cell)) return { ...base, contentAlign: 'right' }
  return base
}

/**
 * Draws the column type, dimmed and right-aligned, after the normal
 * header content when there is room for it.
 */
export function drawTypeLabel(
  ctx: {
    measureText(text: string): { width: number }
    fillText(text: string, x: number, y: number): void
    save(): void
    restore(): void
    fillStyle: unknown
    font: string
    textAlign: string
    textBaseline: string
  },
  rect: { x: number; y: number; width: number; height: number },
  title: string,
  type: string,
  color: string,
): boolean {
  if (!type) return false
  const titleEnd = rect.x + 8 + 20 + ctx.measureText(title).width
  const typeWidth = ctx.measureText(type).width
  const right = rect.x + rect.width - 8
  if (titleEnd + 12 + typeWidth > right) return false
  ctx.save()
  ctx.fillStyle = color
  ctx.font = '11px ' + cssVar('--font-mono', 'monospace')
  ctx.textAlign = 'right'
  ctx.textBaseline = 'middle'
  ctx.fillText(type, right, rect.y + rect.height / 2)
  ctx.restore()
  return true
}

/** Converts Glide's selection into a SelectionSpec for copying. */
export function toSelectionSpec(sel: GridSelection): SelectionSpec {
  if (sel.rows.length > 0) return { rows: sel.rows.toArray() }
  if (sel.columns.length > 0) return { columns: sel.columns.toArray() }
  if (sel.current) return { range: sel.current.range }
  return {}
}

/** Row a selection points at (for the row detail panel). */
export function selectedRowOf(sel: GridSelection): number | null {
  if (sel.current) return sel.current.cell[1]
  if (sel.rows.length > 0) return sel.rows.first() ?? null
  return null
}

/**
 * Read-only data grid (Glide, canvas-rendered). All data mapping, sort
 * clicks and copying live in this wrapper and the helpers above so they
 * are unit-testable with a mocked DataEditor.
 */
export default function DataGrid(props: DataGridProps) {
  const { columns, rows, offset, sort, columnWidths, platform, onHeaderClick, onColumnResize, onSelectRow, onToggleDetail, editing, links, onExportSelection } = props
  const [selection, setSelection] = useState<GridSelection>(emptySelection)
  const [edit, setEditState] = useState<EditingCell | null>(null)
  // Synchronous view of the open editor: keys can reach the grid before
  // the overlay has rendered and taken focus.
  const editRef = useRef<EditingCell | null>(null)
  const editorHandle = useRef<EditorHandle | null>(null)
  const setEdit = (e: EditingCell | null) => {
    editRef.current = e
    setEditState(e)
  }
  const [menu, setMenu] = useState<MenuState | null>(null)
  const gridRef = useRef<DataEditorRef>(null)
  const boxRef = useRef<HTMLDivElement>(null)
  // Recomputed when the theme changes (the CSS variables are already
  // switched by then; see bindAppearance).
  const resolvedTheme = useResolvedTheme()
  const theme = useMemo(() => gridTheme(), [resolvedTheme]) // eslint-disable-line react-hooks/exhaustive-deps
  const muted = theme.textMedium as string

  // A new page (or table) starts without a selection.
  useEffect(() => setSelection(emptySelection), [columns])
  useEffect(() => {
    if (!editing) setEdit(null)
  }, [editing])

  const fkColumns = links?.fkColumns
  const gridColumns = useMemo<GridColumn[]>(
    () =>
      columns.map((c, i) => ({
        id: c.name,
        title: headerTitle(c, sort),
        width: columnWidths[c.name] ?? defaultColumnWidth(c),
        icon: columnIcon(c, Boolean(fkColumns?.[i])),
      })),
    [columns, sort, columnWidths, fkColumns],
  )

  const rowStates = editing?.rowStates
  const failed = editing?.failed ?? null

  // A newly staged row (insert or duplicate) is appended after the fetched
  // rows, often below the fold: bring it into view and select its first
  // editable cell, so typing fills it in straight away.
  const insertCount = rowStates ? countInserted(rowStates) : 0
  const prevInserts = useRef(insertCount)
  useEffect(() => {
    const grew = insertCount > prevInserts.current
    prevInserts.current = insertCount
    if (!grew || !rowStates) return
    const target = newRowCell(rowStates)
    if (!target) return
    const [col, row] = target
    setSelection({ ...emptySelection, current: { cell: [col, row], range: { x: col, y: row, width: 1, height: 1 }, rangeStack: [] } })
    onSelectRow(row)
    gridRef.current?.scrollTo?.(col, row, 'both', 0, 0, { vAlign: 'center' })
    gridRef.current?.focus?.()
  }, [insertCount]) // eslint-disable-line react-hooks/exhaustive-deps
  const getCellContent = useCallback(
    ([col, row]: Item): GridCell => {
      let cell = toGridCell(rows[row]?.[col], muted)
      // The "→" sits at the right edge: keep key values clear of it.
      if (links?.canFollow(row, col)) cell = { ...cell, contentAlign: 'left' } as GridCell
      if (!rowStates) return cell
      const look = cellLook(rowStates[row], col, failed)
      const override: Partial<Theme> = { ...(cell.themeOverride ?? {}) }
      if (look.bg) override.bgCell = look.bg
      if (look.isDefault || look.locked) override.textDark = muted
      if (look.isDefault) override.baseFontStyle = 'italic 12px'
      return Object.keys(override).length ? { ...cell, themeOverride: override } : cell
    },
    [rows, muted, rowStates, failed, links],
  )

  const accent = theme.accentColor as string
  const drawCell = useCallback<DrawCellCallback>(
    (args, drawContent) => {
      drawContent()
      if (links?.canFollow(args.row, args.col)) drawLink(args.ctx, args.rect, accent)
      const look = cellLook(rowStates?.[args.row], args.col, failed)
      const { ctx, rect } = args
      if (look.strike) {
        ctx.save()
        ctx.strokeStyle = cssVar('--danger', '#f85149')
        ctx.lineWidth = 1
        ctx.beginPath()
        ctx.moveTo(rect.x + 4, rect.y + rect.height / 2)
        ctx.lineTo(rect.x + rect.width - 4, rect.y + rect.height / 2)
        ctx.stroke()
        ctx.restore()
      }
      if (look.locked && rowStates) drawLock(ctx, rect.x + rect.width - 12, rect.y + rect.height / 2, muted)
    },
    [rowStates, failed, muted, links, accent],
  )

  const canEditCell = (col: number, row: number) => {
    const d = rowStates?.[row]
    return Boolean(editing && d && d.state !== 'deleted' && !d.locked[col] && columns[col])
  }

  const startEdit = (col: number, row: number, typed?: string) => {
    if (!canEditCell(col, row)) return
    const b = gridRef.current?.getBounds?.(col, row)
    const box = boxRef.current?.getBoundingClientRect()
    const next = { col, row, typed, x: b && box ? b.x - box.left : 0, y: b && box ? b.y - box.top : 0, width: b ? Math.max(b.width, 260) : 320 }
    // Render and focus the editor now, so the next typed key reaches it.
    flushSync(() => {
      setMenu(null)
      setEdit(next)
    })
  }

  const stopEdit = () => {
    setEdit(null)
    gridRef.current?.focus?.()
  }

  const current = selection.current?.cell

  const drawHeader = useCallback<DrawHeaderCallback>(
    (args, drawContent) => {
      drawContent()
      const c = columns[args.columnIndex]
      if (c) drawTypeLabel(args.ctx, args.rect, args.column.title, c.type, muted)
    },
    [columns, muted],
  )

  const onSelectionChange = useCallback(
    (sel: GridSelection) => {
      setSelection(sel)
      onSelectRow(selectedRowOf(sel))
    },
    [onSelectRow],
  )

  /** Grid key handling; returns true when the key was consumed. */
  const handleKey = (e: ModifierEvent): boolean => {
    if (editing && handleEditKey(e)) return true
    if (links && matchesShortcut(e, platform, 'o')) {
      if (current && links.canFollow(current[1], current[0])) links.onFollow(current[1], current[0])
      return true
    }
    if (matchesShortcut(e, platform, 'c')) {
      const tsv = selectionToTSV(columns, rows, toSelectionSpec(selection))
      if (!tsv) return false
      void copyText(tsv)
      return true
    }
    if (e.key === ' ' && !e.metaKey && !e.ctrlKey && !e.altKey) {
      onToggleDetail()
      return true
    }
    return false
  }

  /** Editing keys (see features/workspace/keymap.ts). */
  const handleEditKey = (e: ModifierEvent): boolean => {
    if (!editing) return false
    const open = editRef.current
    if (open) {
      // The grid took focus back while the editor opened: forward the key.
      const h = editorHandle.current
      if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
        h?.type(e.key)
        return true
      }
      if (e.key === 'Escape' || e.key === 'Enter') {
        if (e.key === 'Enter') h?.commit()
        else h?.cancel()
        return true
      }
      return false
    }
    if (matchesShortcut(e, platform, 's')) return editing.onCommit(), true
    if (matchesShortcut(e, platform, 'z')) return editing.onUndo(), true
    if (matchesShortcut(e, platform, 'z', { shift: true })) return editing.onRedo(), true
    if (matchesShortcut(e, platform, 'n')) return editing.onInsertRow(), true
    if (matchesShortcut(e, platform, 'n', { shift: true })) {
      editing.onSetCells(selectedCells(selection, columns.length), { kind: 'null' })
      return true
    }
    if (matchesShortcut(e, platform, 'd')) {
      if (current) editing.onDuplicateRow(current[1])
      return true
    }
    if ((e.key === 'Delete' || e.key === 'Backspace') && !isModPressed(e, platform) && !e.altKey) {
      const rs = selectedRows(selection)
      if (rs.length === 0) return false
      editing.onDeleteRows(rs)
      return true
    }
    if (!current) return false
    if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) {
      startEdit(current[0], current[1])
      return true
    }
    if (isTypingKey(e) && canEditCell(current[0], current[1])) {
      startEdit(current[0], current[1], e.key)
      return true
    }
    return false
  }

  const menuAction = (fn: () => void) => () => {
    fn()
    setMenu(null)
  }
  const menuCells = (m: MenuState): Item[] => {
    const cells = selectedCells(selection, columns.length)
    return cells.some(([c, r]) => c === m.col && r === m.row) ? cells : [[m.col, m.row]]
  }
  /** The selection when the menu's cell is in it, else that cell. */
  const menuSelection = (m: MenuState): SelectionSpec => {
    const inSel = menuCells(m).length > 1 || menuRows(m).length > 1 || selection.columns.length > 0
    return inSel ? toSelectionSpec(selection) : { range: { x: m.col, y: m.row, width: 1, height: 1 } }
  }
  const menuRows = (m: MenuState): number[] => {
    const rs = selectedRows(selection)
    return rs.includes(m.row) ? rs : [m.row]
  }

  const editColumn = edit ? columns[edit.col] : undefined
  const editorStyle: CSSProperties | undefined = edit ? { left: Math.max(0, edit.x), top: Math.max(0, edit.y), width: edit.width } : undefined
  const mod = (k: string, shift = false) => shortcutLabel(platform, k, { shift })

  return (
    <div className="data-grid" data-testid="data-grid" ref={boxRef}>
      <DataEditor
        ref={gridRef}
        columns={gridColumns}
        rows={rows.length}
        getCellContent={getCellContent}
        onHeaderClicked={(col, e) => {
          const c = columns[col]
          if (c) onHeaderClick(c.name, e.shiftKey)
        }}
        onColumnResize={(col, width) => onColumnResize(String(col.id ?? col.title), width)}
        drawCell={editing || links ? drawCell : undefined}
        onCellClicked={
          links
            ? ([col, row], e) => {
                if (e.localEventX >= e.bounds.width - LINK_HIT_WIDTH && links.canFollow(row, col)) links.onFollow(row, col)
              }
            : undefined
        }
        onCellActivated={editing ? ([col, row]) => startEdit(col, row) : undefined}
        onCellContextMenu={
          editing || links || onExportSelection
            ? ([col, row], e) => {
                e.preventDefault()
                setMenu({ col, row, x: e.bounds.x + e.localEventX, y: e.bounds.y + e.localEventY })
              }
            : undefined
        }
        onDelete={() => false}
        onPaste={
          editing
            ? (target, values) => {
                editing.onPaste(target[1], target[0], values)
                return false
              }
            : false
        }
        onKeyDown={(e) => {
          // Glide handles keys itself (and swallows some); ours go first.
          if (handleKey(e)) {
            e.cancel()
            e.preventDefault()
            e.stopPropagation()
          }
        }}
        gridSelection={selection}
        onGridSelectionChange={onSelectionChange}
        rowMarkers={{ kind: 'number', startIndex: offset + 1 }}
        headerIcons={headerIcons}
        drawHeader={drawHeader}
        theme={theme}
        smoothScrollX
        smoothScrollY
        width="100%"
        height="100%"
        rowHeight={26}
        headerHeight={30}
      />
      {edit && editing && editColumn && (
        <div className="cell-editor-overlay" style={editorStyle} data-testid="cell-editor">
          <CellEditor
            key={`${edit.row}:${edit.col}`}
            handle={editorHandle}
            column={editColumn}
            engine={editing.engine}
            text={editing.editText(edit.row, edit.col)}
            typed={edit.typed}
            allowDefault={editing.engine !== 'sqlite' || rowStates?.[edit.row]?.state === 'inserted'}
            commitOnOutsideClick
            onCommit={(v) => {
              editing.onCommitCell(edit.row, edit.col, v)
              stopEdit()
            }}
            onCancel={stopEdit}
          />
        </div>
      )}
      {menu && (editing || links || onExportSelection) && (
        <div className="context-menu-backdrop" onMouseDown={() => setMenu(null)} onContextMenu={(e) => (e.preventDefault(), setMenu(null))}>
          <ul
            className="context-menu"
            role="menu"
            style={{ left: menu.x, top: menu.y }}
            onMouseDown={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.key === 'Escape' && setMenu(null)}
          >
            {links && (
              <MenuItem
                label="Open referenced row"
                hint={mod('O')}
                disabled={!links.canFollow(menu.row, menu.col)}
                onClick={menuAction(() => links.onFollow(menu.row, menu.col))}
              />
            )}
            {links && editing && <li role="separator" />}
            {editing && (
              <>
                <MenuItem label="Edit cell" hint="Enter" disabled={!canEditCell(menu.col, menu.row)} onClick={menuAction(() => startEdit(menu.col, menu.row))} />
                <MenuItem label="Set NULL" hint={mod('N', true)} onClick={menuAction(() => editing.onSetCells(menuCells(menu), { kind: 'null' }))} />
                <MenuItem label="Set DEFAULT" onClick={menuAction(() => editing.onSetCells(menuCells(menu), { kind: 'default' }))} />
                <MenuItem label="Set empty string" onClick={menuAction(() => editing.onSetCells(menuCells(menu), { kind: 'value', text: '' }))} />
                <li role="separator" />
                <MenuItem label="Insert row" hint={mod('N')} onClick={menuAction(editing.onInsertRow)} />
                <MenuItem label="Duplicate row" hint={mod('D')} onClick={menuAction(() => editing.onDuplicateRow(menu.row))} />
                <MenuItem label="Delete row" hint="Del" onClick={menuAction(() => editing.onDeleteRows(menuRows(menu)))} />
              </>
            )}
            {onExportSelection && (
              <>
                {(links || editing) && <li role="separator" />}
                <MenuItem label="Export selection…" onClick={menuAction(() => onExportSelection(menuSelection(menu)))} />
              </>
            )}
          </ul>
        </div>
      )}
    </div>
  )
}

function MenuItem({ label, hint, disabled, onClick }: { label: string; hint?: string; disabled?: boolean; onClick(): void }) {
  return (
    <li role="none">
      <button type="button" role="menuitem" disabled={disabled} onClick={onClick}>
        <span>{label}</span>
        {hint && <span className="hint">{hint}</span>}
      </button>
    </li>
  )
}

/** A small padlock: the cell cannot be edited (truncated value). */
export function drawLock(
  ctx: { save(): void; restore(): void; fillStyle: unknown; strokeStyle: unknown; lineWidth: number; beginPath(): void; arc(x: number, y: number, r: number, a: number, b: number): void; stroke(): void; fillRect(x: number, y: number, w: number, h: number): void },
  x: number,
  y: number,
  color: string,
) {
  ctx.save()
  ctx.strokeStyle = color
  ctx.fillStyle = color
  ctx.lineWidth = 1.2
  ctx.beginPath()
  ctx.arc(x, y - 2, 2.5, Math.PI, 0)
  ctx.stroke()
  ctx.fillRect(x - 3.5, y - 1, 7, 5)
  ctx.restore()
}
