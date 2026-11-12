import type React from 'react'
import { KeyboardEvent, RefObject, useMemo } from 'react'
import { Connection, KIND_BADGES, KIND_LABELS, TAGS, connectionColor, connectionSummary } from './types'

export interface ConnectionListProps {
  connections: Connection[]
  query: string
  onQueryChange(q: string): void
  selectedId: string | null
  onSelect(id: string): void
  onConnect(id: string): void
  onDelete(id: string): void
  connectingId?: string | null
  searchRef?: RefObject<HTMLInputElement>
  searchHint?: string
}

/** Case-insensitive match on name, host, database, file, user, kind and tag. */
export function filterConnections(list: Connection[], query: string): Connection[] {
  const q = query.trim().toLowerCase()
  if (!q) return list
  return list.filter((c) =>
    [c.name, c.host, c.database, c.filePath, c.user, c.kind, KIND_LABELS[c.kind], c.tag]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q)),
  )
}

export default function ConnectionList({
  connections,
  query,
  onQueryChange,
  selectedId,
  onSelect,
  onConnect,
  onDelete,
  connectingId,
  searchRef,
  searchHint,
}: ConnectionListProps) {
  const visible = useMemo(() => filterConnections(connections, query), [connections, query])
  const selectedIndex = visible.findIndex((c) => c.id === selectedId)

  function move(delta: number) {
    if (!visible.length) return
    const next = selectedIndex < 0 ? 0 : Math.min(visible.length - 1, Math.max(0, selectedIndex + delta))
    onSelect(visible[next].id)
  }

  function handleKeyDown(e: KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(1)
        break
      case 'ArrowUp':
        e.preventDefault()
        move(-1)
        break
      case 'Enter': {
        const target = selectedIndex >= 0 ? visible[selectedIndex] : visible.length === 1 ? visible[0] : undefined
        if (target) {
          e.preventDefault()
          onConnect(target.id)
        }
        break
      }
      case 'Delete':
        if (selectedIndex >= 0 && e.target === e.currentTarget) {
          e.preventDefault()
          onDelete(visible[selectedIndex].id)
        }
        break
    }
  }

  return (
    <div className="connection-list">
      <input
        ref={searchRef}
        type="search"
        className="search"
        placeholder={`Search connections${searchHint ? ` (${searchHint})` : ''}`}
        aria-label="Search connections"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter') handleKeyDown(e)
        }}
      />
      {visible.length === 0 ? (
        <p className="list-empty">{connections.length === 0 ? 'No saved connections yet.' : 'No connections match your search.'}</p>
      ) : (
        <ul
          className="connections"
          role="listbox"
          aria-label="Saved connections"
          tabIndex={0}
          aria-activedescendant={selectedIndex >= 0 ? `conn-${visible[selectedIndex].id}` : undefined}
          onKeyDown={handleKeyDown}
        >
          {visible.map((c) => {
            const color = connectionColor(c)
            const tag = TAGS.find((t) => t.value === c.tag)
            return (
              <li
                key={c.id}
                id={`conn-${c.id}`}
                role="option"
                aria-selected={c.id === selectedId}
                className="connection-item"
                onClick={() => onSelect(c.id)}
                onDoubleClick={() => onConnect(c.id)}
              >
                <span className="color-strip" style={color ? { background: color } : undefined} data-testid="color-strip" />
                <span className={`kind-badge kind-${c.kind}`} title={KIND_LABELS[c.kind]}>
                  {KIND_BADGES[c.kind]}
                </span>
                <span className="connection-text">
                  <span className="connection-name">{c.name}</span>
                  <span className="connection-summary mono">{connectionSummary(c)}</span>
                </span>
                {c.tag !== 'none' && tag && (
                  <span className="tag-label" style={{ '--tag': tag.color } as React.CSSProperties}>
                    {tag.label}
                  </span>
                )}
                {connectingId === c.id && <span className="muted">Connecting…</span>}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
