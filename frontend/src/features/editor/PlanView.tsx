import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { copyText } from '../../lib/clipboard'
import { errorMessage } from '../../lib/errors'
import { isModPressed, shortcutLabel } from '../../lib/mod'
import { CaretIcon } from '../../components/icons'
import type { PlanState } from '../../stores/queryTabs'
import { fmtMs, fmtNum, flattenPlan, nodeAt, nodeLine, nodeProps, nodeToText, parentPaths, planToMarkdown, planToText, type PlanRow } from './plan'
import type { PlanNode } from './types'

const ENGINES: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', sqlite: 'SQLite' }

interface Menu {
  path: string
  x: number
  y: number
}

/** Which numeric columns any node fills (the others are hidden). */
function presentColumns(nodes: PlanNode[]) {
  const has = { est: false, actual: false, cost: false, time: false }
  const walk = (ns: PlanNode[]) =>
    ns.forEach((n) => {
      has.est ||= n.estRows !== null
      has.actual ||= n.actualRows !== null || n.loops !== null
      has.cost ||= n.cost !== null
      has.time ||= n.timeMs !== null
      walk(n.children)
    })
  walk(nodes)
  return has
}

function misestimateText(r: PlanRow): string {
  const f = r.estimateFactor ?? 0
  return `Estimated ${fmtNum(r.node.estRows)} rows, actual ${fmtNum(r.node.actualRows)} (${fmtNum(Math.round(f))}× off)`
}

/** The Plan tab: a collapsible tree table of a query plan, its raw output and copy actions. */
export default function PlanView({ state, platform }: { state: PlanState; platform?: string }) {
  const { plan } = state
  const [view, setView] = useState<'tree' | 'raw'>('tree')
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set())
  const [selected, setSelected] = useState<string | null>(plan.nodes.length > 0 ? '0' : null)
  const [menu, setMenu] = useState<Menu | null>(null)
  const [copyMenu, setCopyMenu] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)
  const treeRef = useRef<HTMLTableElement>(null)

  // A new plan starts expanded with its root selected.
  useEffect(() => {
    setCollapsed(new Set())
    setSelected(plan.nodes.length > 0 ? '0' : null)
    setMenu(null)
    setCopied(null)
  }, [plan])

  const rows = useMemo(() => flattenPlan(plan, collapsed), [plan, collapsed])
  const cols = useMemo(() => presentColumns(plan.nodes), [plan])
  const selectedNode = selected ? nodeAt(plan.nodes, selected) : null
  const root = plan.nodes[0]

  const copy = async (text: string, what = 'Copied') => {
    setCopyMenu(false)
    setMenu(null)
    try {
      await copyText(text)
      setCopied(what)
    } catch (err) {
      setCopied(`Copy failed: ${errorMessage(err)}`)
    }
  }

  const toggle = (path: string) =>
    setCollapsed((cur) => {
      const next = new Set(cur)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  const onTreeKey = (e: KeyboardEvent<HTMLTableElement>) => {
    const i = rows.findIndex((r) => r.path === selected)
    const row = rows[i]
    if (isModPressed(e, platform) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'c') {
      if (row) void copy(nodeToText(row.node), 'Copied subtree')
    } else if (e.key === 'ArrowDown') {
      setSelected(rows[Math.min(rows.length - 1, i + 1)]?.path ?? null)
    } else if (e.key === 'ArrowUp') {
      setSelected(rows[Math.max(0, i - 1)]?.path ?? null)
    } else if (e.key === 'ArrowRight' && row?.hasChildren && !row.expanded) {
      toggle(row.path)
    } else if (e.key === 'ArrowLeft' && row?.expanded) {
      toggle(row.path)
    } else {
      return
    }
    e.preventDefault()
    e.stopPropagation()
  }

  const menuNode = menu ? nodeAt(plan.nodes, menu.path) : null
  const summary = [
    plan.planningMs !== null ? `Planning ${fmtMs(plan.planningMs)}` : '',
    plan.executionMs !== null ? `Execution ${fmtMs(plan.executionMs)}` : '',
    root?.estRows != null ? `~${fmtNum(root.estRows)} rows estimated` : '',
    root?.actualRows != null ? `${fmtNum(root.actualRows)} rows` : '',
  ].filter(Boolean)

  return (
    <div className="plan-view">
      <div className="toolbar plan-toolbar">
        <span className="plan-engine">{ENGINES[plan.engine] ?? plan.engine}</span>
        {plan.analyzed && (
          <span className="plan-badge" title="The statement ran: rows and times are actual">
            Analyzed
          </span>
        )}
        {summary.length > 0 && <span className="muted plan-summary">{summary.join(' · ')}</span>}
        <span className="spacer" />
        {copied && (
          <span className="muted" role="status">
            {copied}
          </span>
        )}
        <div className="plan-switch" role="group" aria-label="Plan view">
          <button type="button" aria-pressed={view === 'tree'} onClick={() => setView('tree')}>
            Tree
          </button>
          <button type="button" aria-pressed={view === 'raw'} onClick={() => setView('raw')}>
            Raw
          </button>
        </div>
        {view === 'tree' && plan.nodes.length > 0 && (
          <>
            <button type="button" onClick={() => setCollapsed(new Set())}>
              Expand all
            </button>
            <button type="button" onClick={() => setCollapsed(new Set(parentPaths(plan.nodes)))}>
              Collapse all
            </button>
          </>
        )}
        <div className="split-button">
          <button type="button" title="Copy the plan as indented text" disabled={plan.nodes.length === 0} onClick={() => void copy(planToText(plan))}>
            Copy
          </button>
          <button type="button" className="split-button-menu" aria-label="More copy options" aria-haspopup="menu" aria-expanded={copyMenu} onClick={() => setCopyMenu((o) => !o)}>
            <CaretIcon />
          </button>
          {copyMenu && (
            <div className="context-menu-backdrop" onMouseDown={() => setCopyMenu(false)}>
              <ul className="context-menu split-menu" role="menu" aria-label="Copy options" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === 'Escape' && setCopyMenu(false)}>
                <MenuItem label="Copy as text" disabled={plan.nodes.length === 0} onClick={() => void copy(planToText(plan))} />
                <MenuItem label="Copy raw" onClick={() => void copy(plan.raw)} />
                <MenuItem label="Copy as Markdown table" disabled={plan.nodes.length === 0} onClick={() => void copy(planToMarkdown(plan))} />
                <MenuItem label="Copy query with EXPLAIN" onClick={() => void copy(plan.sql)} />
              </ul>
            </div>
          )}
        </div>
      </div>
      {plan.cancelled && (
        <div className="banner error results-error" role="alert">
          <span>Query cancelled.</span>
        </div>
      )}
      {plan.error && !plan.cancelled && (
        <div className="banner error results-error" role="alert">
          <span className="selectable">
            {plan.error.message}
            {plan.error.detail && <span className="muted"> {plan.error.detail}</span>}
            {plan.error.hint && <span className="muted"> Hint: {plan.error.hint}</span>}
          </span>
        </div>
      )}
      {plan.notices.map((n) => (
        <div key={n} className="banner warning results-error" role="status">
          <span>{n}</span>
        </div>
      ))}
      {view === 'raw' ? (
        <div className="plan-raw">
          <div className="toolbar plan-raw-toolbar">
            <button type="button" onClick={() => void copy(plan.raw)}>
              Copy raw
            </button>
          </div>
          <pre className="selectable" aria-label="Raw plan">
            {plan.raw}
          </pre>
        </div>
      ) : (
        <div className="plan-body">
          <div className="plan-tree-scroll">
            {rows.length === 0 ? (
              !plan.error && !plan.cancelled && <p className="muted plan-empty">No plan.</p>
            ) : (
              <table
                ref={treeRef}
                className="plan-tree"
                role="treegrid"
                aria-label="Query plan"
                tabIndex={0}
                onKeyDown={onTreeKey}
                title={`${shortcutLabel(platform, 'C')} copies the selected node and its subtree`}
              >
                <thead>
                  <tr role="row">
                    <th role="columnheader">Operation</th>
                    <th role="columnheader">Object</th>
                    {cols.est && <th role="columnheader" className="num">Est. rows</th>}
                    {cols.actual && <th role="columnheader" className="num">Actual rows × loops</th>}
                    {cols.cost && <th role="columnheader" className="num">Cost</th>}
                    {cols.time && <th role="columnheader" className="num">Time (ms)</th>}
                    {cols.time && <th role="columnheader">Self</th>}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr
                      key={r.path}
                      role="row"
                      aria-level={r.depth + 1}
                      aria-expanded={r.hasChildren ? r.expanded : undefined}
                      aria-selected={r.path === selected}
                      className={r.hot ? 'hot' : undefined}
                      onClick={() => setSelected(r.path)}
                      onContextMenu={(e) => {
                        e.preventDefault()
                        setSelected(r.path)
                        setMenu({ path: r.path, x: e.clientX, y: e.clientY })
                      }}
                    >
                      <td role="gridcell" className="plan-op" style={{ paddingLeft: 6 + r.depth * 16 }}>
                        {r.hasChildren ? (
                          <button
                            type="button"
                            className={`plan-toggle${r.expanded ? ' open' : ''}`}
                            aria-label={`${r.expanded ? 'Collapse' : 'Expand'} ${r.node.op}`}
                            tabIndex={-1}
                            onClick={(e) => {
                              e.stopPropagation()
                              toggle(r.path)
                            }}
                          >
                            <CaretIcon />
                          </button>
                        ) : (
                          <span className="plan-toggle-space" />
                        )}
                        <span className="plan-op-name">{r.node.op}</span>
                        {r.node.detail && <span className="muted plan-detail"> {r.node.detail}</span>}
                      </td>
                      <td role="gridcell">{r.node.object}</td>
                      {cols.est && (
                        <td role="gridcell" className="num">
                          {fmtNum(r.node.estRows)}
                        </td>
                      )}
                      {cols.actual && (
                        <td role="gridcell" className="num">
                          {r.misestimate && (
                            <span className="plan-warn" title={misestimateText(r)} aria-label={misestimateText(r)}>
                              ⚠{' '}
                            </span>
                          )}
                          {r.node.actualRows === null && r.node.loops === 0 ? (
                            <span className="muted">never executed</span>
                          ) : (
                            <>
                              {fmtNum(r.node.actualRows)}
                              {r.node.loops !== null && r.node.loops !== 1 && <span className="muted"> × {fmtNum(r.node.loops)}</span>}
                            </>
                          )}
                        </td>
                      )}
                      {cols.cost && (
                        <td role="gridcell" className="num">
                          {fmtNum(r.node.cost)}
                        </td>
                      )}
                      {cols.time && (
                        <td role="gridcell" className="num">
                          {fmtNum(r.node.timeMs)}
                        </td>
                      )}
                      {cols.time && (
                        <td role="gridcell" className="plan-self" title={r.selfMs !== null ? `Self ${fmtMs(r.selfMs)}` : undefined}>
                          {r.pctOfTotal !== null && (
                            <>
                              <span className="plan-bar" aria-hidden>
                                <span style={{ width: `${Math.min(100, r.pctOfTotal)}%` }} />
                              </span>
                              <span className="plan-pct">{r.pctOfTotal.toFixed(1)}%</span>
                            </>
                          )}
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          {selectedNode && (
            <aside className="plan-props" aria-label="Node properties">
              <h3>{nodeLine(selectedNode).slice(3)}</h3>
              {selectedNode.props.length === 0 ? (
                <p className="muted">No properties.</p>
              ) : (
                <dl>
                  {selectedNode.props.map((p, i) => (
                    <div key={`${p.key}-${i}`}>
                      <dt>{p.key}</dt>
                      <dd className="selectable">{p.value}</dd>
                    </div>
                  ))}
                </dl>
              )}
            </aside>
          )}
        </div>
      )}
      {menu && menuNode && (
        <div className="context-menu-backdrop" onMouseDown={() => setMenu(null)} onContextMenu={(e) => (e.preventDefault(), setMenu(null))}>
          <ul
            className="context-menu"
            role="menu"
            aria-label={`${menuNode.op} actions`}
            style={{ left: menu.x, top: menu.y }}
            onMouseDown={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setMenu(null)
                treeRef.current?.focus()
              }
            }}
          >
            <MenuItem label="Copy node" onClick={() => void copy(nodeLine(menuNode))} />
            <MenuItem label="Copy subtree" hint={shortcutLabel(platform, 'C')} onClick={() => void copy(nodeToText(menuNode), 'Copied subtree')} />
            <MenuItem label="Copy properties" disabled={menuNode.props.length === 0} onClick={() => void copy(nodeProps(menuNode))} />
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
