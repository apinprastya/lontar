import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../../lib/clipboard'
import { WorkspaceContext, createWorkspaceStore, type ObjectTabState, type TabState, type WorkspaceStore } from '../../stores/workspace'
import type { QueryTabState } from '../../stores/queryTabs'
import { makeObject } from '../../test/objects'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import TabBar from '../workspace/TabBar'
import ObjectIcon from './ObjectIcon'
import ObjectView from './ObjectView'
import type { DbObject, ObjectKind } from './types'

const tick = () => new Promise((r) => setTimeout(r, 0))

const trigger = makeObject({
  kind: 'trigger',
  name: 'orders_touch',
  table: 'orders',
  id: '7',
  detail: 'BEFORE INSERT on orders',
  properties: [
    { key: 'Table', value: 'orders' },
    { key: 'Timing', value: 'BEFORE' },
  ],
})
const fn = makeObject({ schema: 'geo', name: 'add_one', signature: 'x integer', properties: [{ key: 'Returns', value: 'integer' }] })

function api(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
    listDatabases: vi.fn(async () => ['d']),
    useDatabase: vi.fn(),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'orders', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async () => ({ columns: [], rows: [], offset: 0, limit: 300, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s, schema, table) => makeDetail({ schema, name: table })),
    tableDDL: vi.fn(async () => ''),
    listObjects: vi.fn(async () => [trigger]),
    objectDDL: vi.fn(async (_s, _schema, ref) => `CREATE TRIGGER ${ref.name} …;`),
  }
}

describe('ObjectView', () => {
  let store: WorkspaceStore
  let wsApi: WorkspaceApi
  const objectTab = (o: DbObject) => store.getState().tabs.find((t): t is ObjectTabState => t.kind === 'object' && t.object.name === o.name) as ObjectTabState
  const mount = (o: DbObject) => {
    const r = render(
      <WorkspaceContext.Provider value={store}>
        <TabBar />
        <ObjectView tab={objectTab(o)} />
      </WorkspaceContext.Provider>,
    )
    return {
      rerender: () =>
        r.rerender(
          <WorkspaceContext.Provider value={store}>
            <TabBar />
            <ObjectView tab={objectTab(o)} />
          </WorkspaceContext.Provider>,
        ),
    }
  }

  beforeEach(async () => {
    vi.mocked(copyText).mockClear()
    wsApi = api()
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, wsApi)
    await store.getState().init()
  })
  afterEach(cleanup)

  it('shows the details and the DDL, and links a trigger to its table', async () => {
    store.getState().openObject(trigger)
    await act(tick)
    mount(trigger)
    const view = screen.getByTestId('object-view')
    expect(view).toHaveTextContent('orders_touch')
    expect(view).toHaveTextContent('trigger · BEFORE INSERT on orders')
    const details = screen.getByLabelText('Details')
    expect(within(details).getByText('Timing')).toBeInTheDocument()
    expect(within(details).getByText('BEFORE')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'DDL' })).toHaveTextContent('CREATE TRIGGER orders_touch …;')

    fireEvent.click(screen.getByRole('button', { name: 'Copy DDL' }))
    await act(tick)
    expect(copyText).toHaveBeenCalledWith('CREATE TRIGGER orders_touch …;')
    expect(screen.getByRole('status')).toHaveTextContent('Copied')

    fireEvent.click(screen.getByRole('button', { name: 'Open in new query tab' }))
    const q = store.getState().activeTab() as QueryTabState
    expect(q.kind).toBe('query')
    expect(q.sql).toBe('CREATE TRIGGER orders_touch …;')

    fireEvent.click(screen.getByRole('button', { name: 'orders' }))
    await act(tick)
    const t = store.getState().activeTab() as TabState
    expect(t.kind).toBe('table')
    expect(t.table).toBe('orders')
    expect(t.view).toBe('structure')
  })

  it('titles object tabs and closes them', async () => {
    store.getState().openObject(fn)
    await act(tick)
    mount(fn)
    // Another schema: qualified; routines show their arguments.
    const tab = screen.getByRole('tab', { name: /geo\.add_one\(x integer\)/ })
    expect(tab).toHaveAttribute('title', expect.stringContaining('geo.add_one(x integer) (function)'))
    fireEvent.doubleClick(tab)
    expect(screen.queryByRole('textbox', { name: 'Tab name' })).toBeNull()
    expect(screen.queryByLabelText('Details')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Close geo.add_one(x integer)' }))
    expect(store.getState().tabs).toHaveLength(0)
  })

  it('shows loading, errors with Retry, and a failed copy', async () => {
    vi.mocked(wsApi.objectDDL).mockRejectedValueOnce(new Error('object not found'))
    store.getState().openObject({ ...trigger, properties: [] })
    const { rerender } = mount(trigger)
    expect(screen.getByText('Loading DDL…')).toBeInTheDocument()
    await act(tick)
    rerender()
    expect(screen.queryByLabelText('Details')).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent('object not found')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await act(tick)
    rerender()
    expect(screen.queryByRole('alert')).toBeNull()
    vi.mocked(copyText).mockRejectedValueOnce(new Error('no clipboard'))
    fireEvent.click(screen.getByRole('button', { name: 'Copy DDL' }))
    await act(tick)
    expect(screen.getByRole('status')).toHaveTextContent('Copy failed: no clipboard')
  })

  it('has an icon for every kind', () => {
    const kinds: ObjectKind[] = ['function', 'procedure', 'aggregate', 'sequence', 'enum', 'composite', 'domain', 'trigger', 'event']
    const { container } = render(
      <>
        {kinds.map((k) => (
          <ObjectIcon key={k} kind={k} />
        ))}
      </>,
    )
    expect(container.querySelectorAll('svg')).toHaveLength(kinds.length)
    expect(container.querySelector('.object-icon-event')).not.toBeNull()
  })
})
