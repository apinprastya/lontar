import { Command } from 'cmdk'
import { useEffect, useMemo, useState, type KeyboardEvent } from 'react'
import { isModPressed } from '../../lib/mod'
import { commands, shortcutText, type Shortcut } from '../workspace/keymap'
import type { TableType } from '../workspace/types'

export type PaletteMode = 'tables' | 'commands'

/** A table or view offered by quick open. */
export interface PaletteTable {
  schema: string
  name: string
  type: TableType
  /** In the schema the sidebar shows (listed first, unqualified). */
  current: boolean
}

export interface CommandPaletteProps {
  mode: PaletteMode
  platform?: string
  tables: PaletteTable[]
  /** Other schemas are still loading. */
  loading?: boolean
  /** Commands that cannot run now (e.g. Commit without a table tab). */
  disabled?: (id: string) => boolean
  onOpenTable(t: PaletteTable, view: 'data' | 'structure'): void
  onCommand(id: string): void
  onModeChange(mode: PaletteMode): void
  onClose(): void
  /** The command list (defaults to the keymap registry). */
  list?: Shortcut[]
}

/** cmdk value of a table item. */
export function tableValue(t: Pick<PaletteTable, 'schema' | 'name'>): string {
  return `table:${t.schema}\u0000${t.name}`
}

/**
 * Quick open (Mod+P): fuzzy-find tables and views; Enter opens the data,
 * Mod+Enter the structure. Commands (Mod+Shift+P, or ">" in quick open):
 * every command of the keymap registry with its shortcut.
 */
export default function CommandPalette({
  mode,
  platform,
  tables,
  loading,
  disabled,
  onOpenTable,
  onCommand,
  onModeChange,
  onClose,
  list,
}: CommandPaletteProps) {
  const [search, setSearch] = useState('')
  const [value, setValue] = useState('')
  const cmds = useMemo(() => commands(list), [list])
  const byValue = useMemo(() => new Map(tables.map((t) => [tableValue(t), t])), [tables])

  useEffect(() => setSearch(''), [mode])

  function changeSearch(s: string) {
    if (mode === 'tables' && s.startsWith('>')) {
      onModeChange('commands')
      return
    }
    setSearch(s)
  }

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
      return
    }
    if (e.key === 'Backspace' && mode === 'commands' && search === '') {
      e.preventDefault()
      onModeChange('tables')
      return
    }
    if (e.key === 'Enter' && isModPressed(e, platform) && mode === 'tables') {
      e.preventDefault()
      const t = byValue.get(value)
      if (t) onOpenTable(t, 'structure')
    }
  }

  const current = tables.filter((t) => t.current)
  const other = tables.filter((t) => !t.current)
  const tableItem = (t: PaletteTable) => (
    <Command.Item key={tableValue(t)} value={tableValue(t)} keywords={[t.name, `${t.schema}.${t.name}`]} onSelect={() => onOpenTable(t, 'data')}>
      <span className={`palette-kind palette-${t.type.replace(' ', '-')}`} aria-hidden>
        {t.type === 'table' ? 'T' : 'V'}
      </span>
      <span className="palette-name">{t.current ? t.name : `${t.schema}.${t.name}`}</span>
      {t.type !== 'table' && <span className="palette-meta">{t.type}</span>}
    </Command.Item>
  )

  return (
    <div className="dialog-backdrop palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <Command
        className="palette"
        label={mode === 'tables' ? 'Open table' : 'Commands'}
        value={value}
        onValueChange={setValue}
        onKeyDown={onKeyDown}
        loop
      >
        <Command.Input
          autoFocus
          value={search}
          onValueChange={changeSearch}
          placeholder={mode === 'tables' ? 'Open a table or view… (type > for commands)' : 'Run a command…'}
        />
        <Command.List>
          <Command.Empty>{mode === 'tables' ? 'No matching table or view.' : 'No matching command.'}</Command.Empty>
          {mode === 'tables' ? (
            <>
              {current.length > 0 && <Command.Group heading="This schema">{current.map(tableItem)}</Command.Group>}
              {other.length > 0 && <Command.Group heading="Other schemas">{other.map(tableItem)}</Command.Group>}
              {loading && <Command.Loading>Loading other schemas…</Command.Loading>}
            </>
          ) : (
            <Command.Group heading="Commands">
              {cmds.map((c) => (
                <Command.Item key={c.id} value={`cmd:${c.id}`} keywords={[c.description]} disabled={disabled?.(c.id)} onSelect={() => onCommand(c.id)}>
                  <span className="palette-name">{c.description}</span>
                  {c.key && <kbd className="palette-kbd">{shortcutText(c, platform)}</kbd>}
                </Command.Item>
              ))}
            </Command.Group>
          )}
        </Command.List>
        <div className="palette-footer">
          {mode === 'tables' ? (
            <span>
              Enter: data · {shortcutText({ key: 'enter', mod: true }, platform)}: structure · Esc: close
            </span>
          ) : (
            <span>Enter: run · Esc: close</span>
          )}
        </div>
      </Command>
    </div>
  )
}
