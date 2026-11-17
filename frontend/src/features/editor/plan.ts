/**
 * Pure helpers of the query plan view: flattening the tree into table rows
 * (with self time and warning flags) and the text formats used by copy.
 */
import type { Plan, PlanNode } from './types'

/** A node whose own time is at least this share of the total is "hot". */
export const HOT_SHARE = 0.2
/** Estimated and actual rows this many times apart flag a misestimate. */
export const MISESTIMATE_FACTOR = 10

/** One visible row of the plan tree table. */
export interface PlanRow {
  node: PlanNode
  /** Child indexes from the root ("0", "0.1", …); stable across collapsing. */
  path: string
  depth: number
  hasChildren: boolean
  expanded: boolean
  /** Time spent in this node alone (inclusive minus children), when known. */
  selfMs: number | null
  /** selfMs as a percentage of the plan's total time. */
  pctOfTotal: number | null
  hot: boolean
  /** How many times estimated and actual rows are apart (null: unknown). */
  estimateFactor: number | null
  misestimate: boolean
}

/** The node's own time: its inclusive time minus its children's (never below 0). */
export function selfTime(node: PlanNode): number | null {
  if (node.timeMs === null) return null
  const kids = node.children.reduce((sum, c) => sum + (c.timeMs ?? 0), 0)
  return Math.max(0, node.timeMs - kids)
}

/** The plan's total time: the execution time, else the roots' times. */
export function totalTime(plan: Pick<Plan, 'executionMs' | 'nodes'>): number | null {
  if (plan.executionMs !== null && plan.executionMs > 0) return plan.executionMs
  const timed = plan.nodes.filter((n) => n.timeMs !== null)
  if (timed.length === 0) return null
  return timed.reduce((sum, n) => sum + (n.timeMs ?? 0), 0)
}

/** How many times estimated and actual rows are apart (rows below 1 count as 1). */
export function estimateFactor(node: PlanNode): number | null {
  if (node.estRows === null || node.actualRows === null) return null
  const est = Math.max(node.estRows, 1)
  const act = Math.max(node.actualRows, 1)
  return Math.max(est, act) / Math.min(est, act)
}

/** The visible rows of a plan; nodes whose path is in collapsed hide their children. */
export function flattenPlan(plan: Pick<Plan, 'executionMs' | 'nodes'>, collapsed: ReadonlySet<string> = new Set()): PlanRow[] {
  const total = totalTime(plan)
  const out: PlanRow[] = []
  const walk = (nodes: PlanNode[], prefix: string, depth: number) => {
    nodes.forEach((node, i) => {
      const path = prefix ? `${prefix}.${i}` : String(i)
      const self = selfTime(node)
      const pct = self !== null && total ? (self / total) * 100 : null
      const factor = estimateFactor(node)
      const hasChildren = node.children.length > 0
      const expanded = hasChildren && !collapsed.has(path)
      out.push({
        node,
        path,
        depth,
        hasChildren,
        expanded,
        selfMs: self,
        pctOfTotal: pct,
        hot: pct !== null && pct >= HOT_SHARE * 100,
        estimateFactor: factor,
        misestimate: factor !== null && factor >= MISESTIMATE_FACTOR,
      })
      if (expanded) walk(node.children, path, depth + 1)
    })
  }
  walk(plan.nodes, '', 0)
  return out
}

/** Every path of nodes with children (to collapse everything). */
export function parentPaths(nodes: PlanNode[], prefix = ''): string[] {
  return nodes.flatMap((n, i) => {
    const path = prefix ? `${prefix}.${i}` : String(i)
    return n.children.length > 0 ? [path, ...parentPaths(n.children, path)] : []
  })
}

/** The node at a path ("0.1"), if any. */
export function nodeAt(nodes: PlanNode[], path: string): PlanNode | null {
  let cur: PlanNode | null = null
  let list = nodes
  for (const part of path.split('.')) {
    cur = list[Number(part)] ?? null
    if (!cur) return null
    list = cur.children
  }
  return cur
}

/** A number for display: grouped integers, up to 3 decimals ("" when unknown). */
export function fmtNum(n: number | null): string {
  if (n === null || !Number.isFinite(n)) return ''
  const digits = Number.isInteger(n) ? 0 : Math.abs(n) >= 100 ? 1 : Math.abs(n) >= 1 ? 2 : 3
  return n.toLocaleString('en-US', { maximumFractionDigits: digits })
}

/** Milliseconds for display ("" when unknown). */
export function fmtMs(ms: number | null): string {
  return ms === null ? '' : `${fmtNum(ms)} ms`
}

/** "Seq Scan on numbers n (n < 500)". */
export function nodeLabel(node: PlanNode): string {
  return [node.op, node.object ? `on ${node.object}` : '', node.detail].filter(Boolean).join(' ')
}

/** "est=150, actual=150 × 1, cost=27.23, time=0.393 ms" (only the known numbers). */
export function nodeNumbers(node: PlanNode): string {
  const parts: string[] = []
  if (node.estRows !== null) parts.push(`est=${fmtNum(node.estRows)}`)
  if (node.actualRows !== null) parts.push(`actual=${fmtNum(node.actualRows)}${node.loops !== null ? ` × ${fmtNum(node.loops)}` : ''}`)
  else if (node.loops === 0) parts.push('never executed')
  if (node.cost !== null) parts.push(`cost=${fmtNum(node.cost)}`)
  if (node.timeMs !== null) parts.push(`time=${fmtMs(node.timeMs)}`)
  return parts.join(', ')
}

/** One line of the text format: "-> label  (numbers)". */
export function nodeLine(node: PlanNode): string {
  const nums = nodeNumbers(node)
  return `-> ${nodeLabel(node)}${nums ? `  (${nums})` : ''}`
}

/** A node and its subtree as indented text, one line per node. */
export function nodeToText(node: PlanNode, depth = 0): string {
  const lines: string[] = []
  const walk = (n: PlanNode, d: number) => {
    lines.push('    '.repeat(d) + nodeLine(n))
    n.children.forEach((c) => walk(c, d + 1))
  }
  walk(node, depth)
  return lines.join('\n')
}

/** The whole plan as indented text, followed by the planning and execution times. */
export function planToText(plan: Pick<Plan, 'nodes' | 'planningMs' | 'executionMs'>): string {
  const lines = plan.nodes.map((n) => nodeToText(n))
  if (plan.planningMs !== null) lines.push(`Planning time: ${fmtMs(plan.planningMs)}`)
  if (plan.executionMs !== null) lines.push(`Execution time: ${fmtMs(plan.executionMs)}`)
  return lines.join('\n')
}

const mdCell = (s: string) => s.replace(/\\/g, '\\\\').replace(/\|/g, '\\|').replace(/\r?\n/g, ' ')

/** The plan as a Markdown table with the tree table's columns (indentation in the Operation cell). */
export function planToMarkdown(plan: Pick<Plan, 'nodes' | 'planningMs' | 'executionMs'>): string {
  const head = ['Operation', 'Object', 'Detail', 'Est. rows', 'Actual rows', 'Loops', 'Cost', 'Time (ms)', 'Self %']
  const lines = [`| ${head.join(' | ')} |`, `|${head.map((_, i) => (i >= 3 ? ' ---: ' : ' --- ')).join('|')}|`]
  for (const r of flattenPlan(plan)) {
    const n = r.node
    const indent = r.depth > 0 ? '  '.repeat(r.depth - 1) + '└ ' : ''
    const cells = [
      indent + n.op,
      n.object,
      n.detail,
      fmtNum(n.estRows),
      fmtNum(n.actualRows),
      fmtNum(n.loops),
      fmtNum(n.cost),
      fmtNum(n.timeMs),
      r.pctOfTotal === null ? '' : `${r.pctOfTotal.toFixed(1)}%`,
    ]
    lines.push(`| ${cells.map(mdCell).join(' | ')} |`)
  }
  const times = [
    plan.planningMs !== null ? `Planning time: ${fmtMs(plan.planningMs)}` : '',
    plan.executionMs !== null ? `Execution time: ${fmtMs(plan.executionMs)}` : '',
  ].filter(Boolean)
  if (times.length) lines.push('', times.join(' · '))
  return lines.join('\n')
}

/** A node's properties as tab-separated key/value lines. */
export function nodeProps(node: PlanNode): string {
  return node.props.map((p) => `${p.key}\t${p.value.replace(/[\t\r\n]+/g, ' ')}`).join('\n')
}
