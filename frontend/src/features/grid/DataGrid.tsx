import {
  CompactSelection,
  DataEditor,
  GridCellKind,
  GridColumnIcon,
  type DrawHeaderCallback,
  type GridCell,
  type GridColumn,
  type GridSelection,
  type Item,
  type Theme,
} from '@glideapps/glide-data-grid'
import '@glideapps/glide-data-grid/dist/index.css'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { copyText } from '../../lib/clipboard'
import { matchesShortcut, type ModifierEvent } from '../../lib/mod'
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
}

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
    textHeaderSelected: '#ffffff',
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

/** Header icon by column type. */
export function columnIcon(column: ResultColumn): string {
  if (column.isPK) return 'pk'
  const t = column.type.toLowerCase()
  if (/int|num|dec|float|double|real|serial|money/.test(t)) return GridColumnIcon.HeaderNumber
  if (/bool/.test(t)) return GridColumnIcon.HeaderBoolean
  if (/date|time/.test(t)) return GridColumnIcon.HeaderDate
  if (/json/.test(t)) return GridColumnIcon.HeaderCode
  if (/bin|blob|bytea/.test(t)) return GridColumnIcon.HeaderImage
  return GridColumnIcon.HeaderString
}

/** Key icon for primary-key columns. */
export const headerIcons = {
  pk: () =>
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="7" cy="10" r="3.5" fill="none" stroke="#d29922" stroke-width="2"/><path d="M10.5 10h7m-2 0v3m-2.5-3v2" stroke="#d29922" stroke-width="2" fill="none" stroke-linecap="round"/></svg>',
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
  const { columns, rows, offset, sort, columnWidths, platform, onHeaderClick, onColumnResize, onSelectRow, onToggleDetail } = props
  const [selection, setSelection] = useState<GridSelection>(emptySelection)
  const theme = useMemo(() => gridTheme(), [])
  const muted = theme.textMedium as string

  // A new page (or table) starts without a selection.
  useEffect(() => setSelection(emptySelection), [rows, columns])

  const gridColumns = useMemo<GridColumn[]>(
    () =>
      columns.map((c) => ({
        id: c.name,
        title: headerTitle(c, sort),
        width: columnWidths[c.name] ?? defaultColumnWidth(c),
        icon: columnIcon(c),
      })),
    [columns, sort, columnWidths],
  )

  const getCellContent = useCallback(([col, row]: Item): GridCell => toGridCell(rows[row]?.[col], muted), [rows, muted])

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

  return (
    <div className="data-grid" data-testid="data-grid">
      <DataEditor
        columns={gridColumns}
        rows={rows.length}
        getCellContent={getCellContent}
        onHeaderClicked={(col, e) => {
          const c = columns[col]
          if (c) onHeaderClick(c.name, e.shiftKey)
        }}
        onColumnResize={(col, width) => onColumnResize(String(col.id ?? col.title), width)}
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
    </div>
  )
}
