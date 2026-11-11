import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeConnection } from '../../test/mockBindings'
import ConnectionList, { filterConnections } from './ConnectionList'
import type { Connection } from './types'

const conns: Connection[] = [
  makeConnection({ id: 'a', name: 'Analytics', kind: 'postgres', host: 'pg.internal', port: 5432, database: 'warehouse', tag: 'prod' }),
  makeConnection({ id: 'b', name: 'Blog', kind: 'mysql', host: 'localhost', port: 3306, database: 'wp', tag: 'local', color: '#a371f7' }),
  makeConnection({ id: 'c', name: 'Cache', kind: 'sqlite', host: undefined, port: undefined, database: undefined, filePath: '/tmp/cache.db', tag: 'none' }),
]

function Harness(props: { onConnect?: (id: string) => void; onDelete?: (id: string) => void; list?: Connection[]; connectingId?: string }) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  return (
    <ConnectionList
      connections={props.list ?? conns}
      query={query}
      onQueryChange={setQuery}
      selectedId={selected}
      onSelect={setSelected}
      onConnect={props.onConnect ?? vi.fn()}
      onDelete={props.onDelete ?? vi.fn()}
      connectingId={props.connectingId}
      searchHint="Ctrl+F"
    />
  )
}

describe('ConnectionList', () => {
  afterEach(cleanup)

  it('renders name, kind badge, summary, tag and colour strip', () => {
    render(<Harness connectingId="a" />)
    const items = screen.getAllByRole('option')
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent('Analytics')
    expect(items[0]).toHaveTextContent('PG')
    expect(items[0]).toHaveTextContent('pg.internal:5432/warehouse')
    expect(items[0]).toHaveTextContent('Production')
    expect(items[0]).toHaveTextContent('Connecting…')
    expect(items[2]).toHaveTextContent('/tmp/cache.db')
    expect(items[2]).not.toHaveTextContent('None')
    const strips = screen.getAllByTestId('color-strip')
    expect(strips[0]).toHaveStyle({ background: '#f85149' })
    expect(strips[1]).toHaveStyle({ background: '#a371f7' })
    expect(strips[2].getAttribute('style')).toBeNull()
    expect(screen.getByPlaceholderText('Search connections (Ctrl+F)')).toBeInTheDocument()
  })

  it('filters by name, host, kind and tag', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    const search = screen.getByLabelText('Search connections')
    await user.type(search, 'blo')
    expect(screen.getAllByRole('option').map((o) => o.id)).toEqual(['conn-b'])
    await user.clear(search)
    await user.type(search, 'PG.INTERNAL')
    expect(screen.getAllByRole('option').map((o) => o.id)).toEqual(['conn-a'])
    await user.clear(search)
    await user.type(search, 'sqlite')
    expect(screen.getAllByRole('option').map((o) => o.id)).toEqual(['conn-c'])
    await user.clear(search)
    await user.type(search, 'zzz')
    expect(screen.getByText('No connections match your search.')).toBeInTheDocument()
  })

  it('filterConnections matches tags and trims the query', () => {
    expect(filterConnections(conns, ' prod ').map((c) => c.id)).toEqual(['a'])
    expect(filterConnections(conns, '')).toBe(conns)
  })

  it('shows an empty state', () => {
    render(<Harness list={[]} />)
    expect(screen.getByText('No saved connections yet.')).toBeInTheDocument()
  })

  it('selects on click and connects on double-click', async () => {
    const onConnect = vi.fn()
    const user = userEvent.setup()
    render(<Harness onConnect={onConnect} />)
    const [a, b] = screen.getAllByRole('option')
    await user.click(b)
    expect(b).toHaveAttribute('aria-selected', 'true')
    expect(a).toHaveAttribute('aria-selected', 'false')
    await user.dblClick(a)
    expect(onConnect).toHaveBeenCalledWith('a')
  })

  it('navigates with arrows, connects with Enter and deletes with Delete', async () => {
    const onConnect = vi.fn()
    const onDelete = vi.fn()
    const user = userEvent.setup()
    render(<Harness onConnect={onConnect} onDelete={onDelete} />)
    const list = screen.getByRole('listbox')
    list.focus()
    await user.keyboard('{Enter}') // nothing selected, several items
    expect(onConnect).not.toHaveBeenCalled()
    await user.keyboard('{Delete}')
    expect(onDelete).not.toHaveBeenCalled()

    await user.keyboard('{ArrowDown}')
    expect(list).toHaveAttribute('aria-activedescendant', 'conn-a')
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}')
    expect(list).toHaveAttribute('aria-activedescendant', 'conn-c')
    await user.keyboard('{ArrowUp}')
    expect(list).toHaveAttribute('aria-activedescendant', 'conn-b')
    await user.keyboard('{Enter}')
    expect(onConnect).toHaveBeenCalledWith('b')
    await user.keyboard('{Delete}')
    expect(onDelete).toHaveBeenCalledWith('b')
    fireEvent.keyDown(list, { key: 'x' })
  })

  it('connects the only match with Enter from the search box', async () => {
    const onConnect = vi.fn()
    const user = userEvent.setup()
    render(<Harness onConnect={onConnect} />)
    const search = screen.getByLabelText('Search connections')
    await user.type(search, 'cache{Enter}')
    expect(onConnect).toHaveBeenCalledWith('c')
    await user.clear(search)
    await user.type(search, '{ArrowDown}{ArrowUp}{Enter}x')
    expect(onConnect).toHaveBeenLastCalledWith('a')
  })

  it('ignores arrows on an empty result', async () => {
    const user = userEvent.setup()
    render(<Harness />)
    const search = screen.getByLabelText('Search connections')
    await user.type(search, 'zzz{ArrowDown}')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })
})
