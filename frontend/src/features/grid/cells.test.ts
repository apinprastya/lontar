import { describe, expect, it } from 'vitest'
import type { Cell, ResultColumn } from '../workspace/types'
import { binaryLabel, cellCopyText, cellDisplay, cellText, defaultColumnWidth, headerTitle, isNull, isNumeric, selectionToTSV, selectionToCSV, MAX_CELL_CHARS } from './cells'

const cols: ResultColumn[] = [
  { name: 'id', type: 'int', isPK: true, nullable: false },
  { name: 'name', type: 'text', isPK: false, nullable: true },
  { name: 'data', type: 'bytea', isPK: false, nullable: true },
]
const rows: Cell[][] = [
  [{ k: 'num', v: 1 }, { k: 'str', v: 'a\tb' }, { k: 'bytes', v: '00ff', len: 2 }],
  [{ k: 'num', v: '9007199254740993' }, { k: 'null' }, { k: 'null' }],
  [{ k: 'num', v: 3 }, { k: 'str', v: '' }, { k: 'bytes', v: '', len: 1 }],
]

describe('cell text', () => {
  it('renders NULL, empty strings, numbers, bytes and booleans', () => {
    expect(cellText({ k: 'null' })).toBe('NULL')
    expect(cellText(undefined)).toBe('NULL')
    expect(cellText({ k: 'str', v: '' })).toBe('')
    expect(cellText({ k: 'num', v: '9007199254740993' })).toBe('9007199254740993')
    expect(cellText({ k: 'bool', v: false })).toBe('false')
    expect(cellText({ k: 'str' })).toBe('')
    expect(cellText({ k: 'bytes', v: 'ab', len: 1234 })).toBe('<binary 1,234 bytes>')
    expect(binaryLabel({ k: 'bytes', len: 1 })).toBe('<binary 1 byte>')
    expect(binaryLabel({ k: 'bytes' })).toBe('<binary 0 bytes>')
  })

  it('flattens and truncates long text for the grid', () => {
    expect(cellDisplay({ k: 'str', v: 'a\nb\r\nc' })).toBe('a↵ b↵ c')
    const long = cellDisplay({ k: 'str', v: 'x'.repeat(MAX_CELL_CHARS + 50) })
    expect(long).toHaveLength(MAX_CELL_CHARS + 1)
    expect(long.endsWith('…')).toBe(true)
    expect(cellDisplay({ k: 'null' })).toBe('NULL')
    expect(cellDisplay({ k: 'bytes', len: 3 })).toBe('<binary 3 bytes>')
  })

  it('classifies cells', () => {
    expect(isNull(undefined)).toBe(true)
    expect(isNull({ k: 'str', v: '' })).toBe(false)
    expect(isNumeric({ k: 'num', v: 1 })).toBe(true)
    expect(isNumeric({ k: 'str', v: '1' })).toBe(false)
  })

  it('copies NULL as empty and bytes as hex', () => {
    expect(cellCopyText({ k: 'null' })).toBe('')
    expect(cellCopyText({ k: 'bytes', v: '00ff', len: 2 })).toBe('00ff')
    expect(cellCopyText({ k: 'bytes', len: 2 })).toBe('')
    expect(cellCopyText({ k: 'str', v: 'a\tb\nc' })).toBe('a b c')
  })
})

describe('selectionToTSV', () => {
  it('copies a cell range', () => {
    expect(selectionToTSV(cols, rows, { range: { x: 0, y: 0, width: 2, height: 2 } })).toBe('1\ta b\n9007199254740993\t')
  })
  it('copies whole rows', () => {
    expect(selectionToTSV(cols, rows, { rows: [2] })).toBe('3\t\t')
  })
  it('copies whole columns with a header', () => {
    expect(selectionToTSV(cols, rows, { columns: [2] })).toBe('data\n00ff\n\n')
  })
  it('clips out-of-range selections and handles empty ones', () => {
    expect(selectionToTSV(cols, rows, { range: { x: 2, y: 2, width: 5, height: 5 } })).toBe('')
    expect(selectionToTSV(cols, rows, {})).toBe('')
    expect(selectionToTSV(cols, rows, { rows: [] })).toBe('')
    expect(selectionToTSV(cols, rows, { range: { x: 1, y: 2, width: 1, height: 9 } })).toBe('')
  })
})

describe('headers', () => {
  it('shows sort arrows and positions', () => {
    expect(headerTitle(cols[0], [])).toBe('id')
    expect(headerTitle(cols[0], [{ column: 'id', desc: false }])).toBe('id ▲')
    expect(headerTitle(cols[1], [{ column: 'id', desc: false }, { column: 'name', desc: true }])).toBe('name ▼2')
  })
  it('sizes columns from name and type', () => {
    expect(defaultColumnWidth(cols[0])).toBe(120)
    expect(defaultColumnWidth({ name: 'x'.repeat(100), type: 't', isPK: false, nullable: true })).toBe(320)
  })
})

describe('selectionToCSV', () => {
  const cols = [
    { name: 'id', type: 'int', isPK: true, nullable: false },
    { name: 'say "x"', type: 'text', isPK: false, nullable: true },
  ]
  const rows: Cell[][] = [
    [{ k: 'num', v: 1 }, { k: 'str', v: 'a,b\nc' }],
    [{ k: 'num', v: 2 }, { k: 'null' }],
    [{ k: 'num', v: 3 }, { k: 'bytes', v: 'ff00', len: 2 }],
    [{ k: 'num', v: 4 }, { k: 'str', v: '' }],
  ]
  it('quotes when needed, keeps values intact and marks NULL', () => {
    const csv = selectionToCSV(cols, rows, { rows: [0, 1, 2, 3] }, { delimiter: ',', quote: '"', header: true, nullText: '', crlf: true })
    expect(csv).toBe('id,"say ""x"""\r\n1,"a,b\nc"\r\n2,\r\n3,ff00\r\n4,""\r\n')
    expect(selectionToCSV(cols, rows, { range: { x: 1, y: 1, width: 1, height: 1 } }, { delimiter: '\t', quote: '"', header: false, nullText: '\\N', crlf: false })).toBe('\\N\n')
    expect(selectionToCSV(cols, rows, {}, { delimiter: ',', quote: '"', header: true, nullText: '', crlf: false })).toBe('')
  })
})
