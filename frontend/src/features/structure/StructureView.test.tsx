import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { EditorView } from '@codemirror/view'
import { copyText } from '../../lib/clipboard'
import { WorkspaceContext, createWorkspaceStore, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { makeColumn, makeDetail, makeFK } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import StructureView from './StructureView'
import type { TableDetail } from './types'

const tick = () => new Promise((r) => setTimeout(r, 0))

const detail: TableDetail = makeDetail({
  schema: 'public',
  name: 'orders',
  comment: 'All orders',
  estimatedRows: 1500,
  sizeBytes: 16384,
  engine: 'InnoDB',
  collation: 'utf8mb4_bin',
  columns: [
    makeColumn({ name: 'id', ordinal: 1, dataType: 'bigint', nullable: false, isPrimaryKey: true, pkPosition: 1, autoIncrement: true, identity: 'ALWAYS' }),
    makeColumn({ name: 'customer_id', ordinal: 2, dataType: 'integer', comment: 'Buyer' }),
    makeColumn({ name: 'total', ordinal: 3, dataType: 'numeric(12,2)', generated: 'STORED', generationExpr: 'qty * price', collation: '' }),
    makeColumn({ name: 'note', ordinal: 4, dataType: 'text', default: "''::text", collation: 'C' }),
  ],
  indexes: [
    { name: 'orders_pkey', columns: ['id'], unique: true, primary: true, method: 'btree', predicate: '', definition: 'CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)' },
    { name: 'orders_big', columns: ['(lower(note))'], unique: false, primary: false, method: 'gin', predicate: 'total > 100', definition: 'CREATE INDEX orders_big …' },
  ],
  foreignKeys: [makeFK({ name: 'orders_customer_fk', table: 'orders', columns: ['customer_id'], refSchema: 'sales', refTable: 'customers', refColumns: ['id'], onDelete: 'CASCADE', onUpdate: 'SET NULL' })],
  referencedBy: [makeFK({ name: '', table: 'order_lines', columns: ['order_id'], refTable: 'orders', refColumns: ['id'] })],
  checks: [{ name: 'orders_total_check', expression: 'total >= 0' }],
  triggers: [{ name: 'orders_touch', timing: 'BEFORE', event: 'INSERT OR UPDATE', definition: 'CREATE TRIGGER orders_touch …' }],
})

function api(over: Partial<WorkspaceApi> = {}): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
    listDatabases: vi.fn(async () => ['d']),
    useDatabase: vi.fn(),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'orders', type: 'table' as const, estimatedRows: 2 }]),
    fetchTable: vi.fn(async () => ({ columns: [], rows: [], offset: 0, limit: 300, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => detail),
    tableDDL: vi.fn(async () => 'CREATE TABLE public.orders (\n    id bigint\n);\n'),
    ...over,
  }
}

describe('StructureView', () => {
  let store: WorkspaceStore
  let wsApi: WorkspaceApi
  const tab = () => store.getState().tabs.find((t): t is TabState => t.kind === 'table' && t.table === 'orders') as TabState
  const mount = () =>
    render(
      <WorkspaceContext.Provider value={store}>
        <StructureView tab={tab()} />
      </WorkspaceContext.Provider>,
    )
  const rerender = (r: ReturnType<typeof mount>) =>
    r.rerender(
      <WorkspaceContext.Provider value={store}>
        <StructureView tab={tab()} />
      </WorkspaceContext.Provider>,
    )
  const section = async (r: ReturnType<typeof mount>, name: RegExp) => {
    fireEvent.click(screen.getByRole('tab', { name }))
    await act(tick)
    rerender(r)
  }

  async function setup(over: Partial<WorkspaceApi> = {}) {
    wsApi = api(over)
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, wsApi)
    await store.getState().init()
    store.getState().openTableView('public', 'orders', 'structure')
    await act(tick)
  }
  beforeEach(() => vi.mocked(copyText).mockClear())
  afterEach(cleanup)

  it('shows the metadata header and the columns', async () => {
    await setup()
    mount()
    expect(screen.getByText('orders')).toBeInTheDocument()
    expect(screen.getByText('table · ~1.5K rows · 16 KiB · InnoDB · utf8mb4_bin')).toBeInTheDocument()
    expect(screen.getByText('All orders')).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Columns' })
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(5)
    expect(rows[1]).toHaveTextContent('id')
    expect(rows[1]).toHaveTextContent('bigint')
    expect(rows[1]).toHaveTextContent('NO')
    expect(rows[1]).toHaveTextContent('identity (always)')
    expect(within(rows[1]).getByTitle('Primary key (1)')).toBeInTheDocument()
    expect(within(rows[2]).getByTitle('Foreign key')).toBeInTheDocument()
    expect(rows[2]).toHaveTextContent('Buyer')
    expect(rows[3]).toHaveTextContent('generated stored: qty * price')
    expect(rows[4]).toHaveTextContent("''::text")
    expect(rows[4]).toHaveTextContent('C')
    expect(screen.getByRole('tab', { name: /Columns/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: /Foreign keys/ })).toHaveTextContent('1')
  })

  it('shows indexes, keys with links, checks and triggers', async () => {
    await setup()
    const r = mount()
    await section(r, /Indexes/)
    const ix = screen.getByRole('table', { name: 'Indexes' })
    expect(ix).toHaveTextContent('orders_pkey')
    expect(ix).toHaveTextContent('(lower(note)) WHERE total > 100')
    expect(ix).toHaveTextContent('gin')
    expect(within(ix).getByTitle('Primary key')).toBeInTheDocument()

    await section(r, /Foreign keys/)
    const fks = screen.getByRole('table', { name: 'Foreign keys' })
    expect(fks).toHaveTextContent('orders_customer_fk')
    expect(fks).toHaveTextContent('SET NULL')
    expect(fks).toHaveTextContent('CASCADE')
    // Another schema: qualified; the link opens that table's tab.
    fireEvent.click(within(fks).getByRole('button', { name: 'sales.customers' }))
    await act(tick)
    const opened = store.getState().tabs.find((t) => t.kind === 'table' && t.table === 'customers') as TabState
    expect(opened.schema).toBe('sales')
    expect(store.getState().activeTabId).toBe(opened.id)

    await section(r, /Referenced by/)
    const refs = screen.getByRole('table', { name: 'Referenced by' })
    expect(refs).toHaveTextContent('order_id → id')
    fireEvent.click(within(refs).getByRole('button', { name: 'order_lines' }))
    expect(store.getState().tabs.some((t) => t.kind === 'table' && t.table === 'order_lines')).toBe(true)

    await section(r, /Checks/)
    expect(screen.getByRole('table', { name: 'Checks' })).toHaveTextContent('total >= 0')
    await section(r, /Triggers/)
    expect(screen.getByRole('table', { name: 'Triggers' })).toHaveTextContent('INSERT OR UPDATE')
  })

  it('shows empty sections and unsupported checks', async () => {
    await setup({ tableInfo: vi.fn(async () => makeDetail({ name: 'orders', checksSupported: false })) })
    const r = mount()
    await section(r, /Indexes/)
    expect(screen.getByText('No indexes.')).toBeInTheDocument()
    await section(r, /Foreign keys/)
    expect(screen.getByText('No foreign keys.')).toBeInTheDocument()
    await section(r, /Referenced by/)
    expect(screen.getByText('No foreign keys point at this table.')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Checks' }).querySelector('.count')).toBeNull()
    await section(r, /Checks/)
    expect(screen.getByText(/does not report check constraints/)).toBeInTheDocument()
    await section(r, /Triggers/)
    expect(screen.getByText('No triggers.')).toBeInTheDocument()
    store.getState().loadStructure(tab().id)
    rerender(r)
    expect(screen.getByText('Refreshing…')).toBeInTheDocument()
  })

  it('shows no checks when there are none', async () => {
    await setup({ tableInfo: vi.fn(async () => makeDetail({ name: 'orders' })) })
    const r = mount()
    await section(r, /Checks/)
    expect(screen.getByText('No check constraints.')).toBeInTheDocument()
  })

  it('shows the DDL read-only with copy and open in a query tab', async () => {
    await setup()
    const r = mount()
    fireEvent.click(screen.getByRole('tab', { name: 'DDL' }))
    rerender(r)
    expect(screen.getByText('Loading DDL…')).toBeInTheDocument()
    await act(tick)
    rerender(r)
    const cm = screen.getByTestId('ddl-view').querySelector('.cm-editor') as HTMLElement
    const view = EditorView.findFromDOM(cm) as EditorView
    expect(view.state.doc.toString()).toBe('CREATE TABLE public.orders (\n    id bigint\n);\n')
    expect(view.state.readOnly).toBe(true)
    expect(cm.querySelector('.cm-lineNumbers')).not.toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await act(tick)
    expect(copyText).toHaveBeenCalledWith('CREATE TABLE public.orders (\n    id bigint\n);\n')
    expect(screen.getByRole('status')).toHaveTextContent('Copied')
    vi.mocked(copyText).mockRejectedValueOnce(new Error('no clipboard'))
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await act(tick)
    expect(screen.getByRole('status')).toHaveTextContent('Copy failed: no clipboard')

    fireEvent.click(screen.getByRole('button', { name: 'Open in new query tab' }))
    const q = store.getState().tabs.find((t) => t.kind === 'query')
    expect(q?.kind === 'query' && q.sql).toBe('CREATE TABLE public.orders (\n    id bigint\n);\n')
    expect(store.getState().activeTabId).toBe(q?.id)
  })

  it('shows DDL and structure errors with retry', async () => {
    await setup({ tableDDL: vi.fn(async () => Promise.reject(new Error('ddl boom'))) })
    const r = mount()
    await section(r, /DDL/)
    expect(screen.getByRole('alert')).toHaveTextContent('ddl boom')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(wsApi.tableDDL).toHaveBeenCalledTimes(2)
    cleanup()

    await setup({ tableInfo: vi.fn(async () => Promise.reject(new Error('info boom'))) })
    mount()
    expect(screen.getByRole('alert')).toHaveTextContent('info boom')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(wsApi.tableInfo).toHaveBeenCalledTimes(2)
  })

  it('shows a loading state', async () => {
    await setup({ tableInfo: vi.fn(() => new Promise<TableDetail>(() => {})) })
    mount()
    expect(screen.getByText('Loading structure…')).toBeInTheDocument()
  })
})
