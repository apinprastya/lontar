import { describe, expect, it } from 'vitest'
import { planNode, samplePlan } from '../../test/fakeEditorApi'
import {
  estimateFactor,
  flattenPlan,
  fmtMs,
  fmtNum,
  nodeAt,
  nodeLabel,
  nodeLine,
  nodeNumbers,
  nodeProps,
  nodeToText,
  parentPaths,
  planToMarkdown,
  planToText,
  selfTime,
  totalTime,
} from './plan'

/** A SQLite plan: details only, no numbers. */
function sqlitePlan() {
  return samplePlan({
    engine: 'sqlite',
    analyzed: false,
    planningMs: null,
    executionMs: null,
    nodes: [planNode('SCAN', { object: 'n' }), planNode('CORRELATED SCALAR SUBQUERY 1', { children: [planNode('SEARCH', { object: 'i', detail: 'USING INTEGER PRIMARY KEY (rowid=?)' })] })],
  })
}

describe('plan helpers', () => {
  it('flattens the tree with depth, self time and flags', () => {
    const rows = flattenPlan(samplePlan())
    expect(rows.map((r) => `${r.path}:${r.depth}:${r.node.op}`)).toEqual(['0:0:Sort', '0.0:1:Hash Join', '0.0.0:2:Seq Scan', '0.0.1:2:Hash', '0.0.1.0:3:Seq Scan'])
    const [sort, join, numbers, hash, items] = rows
    expect(sort.selfMs).toBe(3)
    expect(sort.pctOfTotal).toBeCloseTo(30)
    expect(sort.hot).toBe(true)
    expect(join.selfMs).toBeCloseTo(0.98)
    expect(join.hot).toBe(false)
    expect(numbers.hot).toBe(true)
    expect(numbers.misestimate).toBe(true)
    expect(numbers.estimateFactor).toBeCloseTo(99.8)
    expect(hash.misestimate).toBe(false)
    expect(sort.hasChildren && sort.expanded).toBe(true)
    expect(items.hasChildren).toBe(false)
  })

  it('collapses subtrees by path', () => {
    const rows = flattenPlan(samplePlan(), new Set(['0.0']))
    expect(rows.map((r) => r.path)).toEqual(['0', '0.0'])
    expect(rows[1].expanded).toBe(false)
    expect(parentPaths(samplePlan().nodes)).toEqual(['0', '0.0', '0.0.1'])
  })

  it('handles plans without numbers (SQLite)', () => {
    const rows = flattenPlan(sqlitePlan())
    expect(rows).toHaveLength(3)
    expect(rows.every((r) => r.selfMs === null && r.pctOfTotal === null && !r.hot && !r.misestimate && r.estimateFactor === null)).toBe(true)
    expect(totalTime(sqlitePlan())).toBeNull()
  })

  it('computes self and total time', () => {
    expect(selfTime(planNode('x'))).toBeNull()
    // Children may report more than the parent (parallel workers): never negative.
    expect(selfTime(planNode('x', { timeMs: 1, children: [planNode('y', { timeMs: 3 }), planNode('z')] }))).toBe(0)
    expect(totalTime({ executionMs: null, nodes: [planNode('a', { timeMs: 2 }), planNode('b'), planNode('c', { timeMs: 1 })] })).toBe(3)
    expect(totalTime({ executionMs: 0, nodes: [planNode('a', { timeMs: 2 })] })).toBe(2)
    expect(estimateFactor(planNode('x', { estRows: 0, actualRows: 0.003 }))).toBe(1)
    expect(estimateFactor(planNode('x', { estRows: 100, actualRows: 10 }))).toBe(10)
  })

  it('finds nodes by path', () => {
    const { nodes } = samplePlan()
    expect(nodeAt(nodes, '0.0.1.0')?.object).toBe('items i')
    expect(nodeAt(nodes, '0.5')).toBeNull()
    expect(nodeAt(nodes, '3')).toBeNull()
  })

  it('formats numbers', () => {
    expect(fmtNum(null)).toBe('')
    expect(fmtNum(Infinity)).toBe('')
    expect(fmtNum(1234567)).toBe('1,234,567')
    expect(fmtNum(1234.56)).toBe('1,234.6')
    expect(fmtNum(27.234)).toBe('27.23')
    expect(fmtNum(0.0034)).toBe('0.003')
    expect(fmtMs(null)).toBe('')
    expect(fmtMs(0.393)).toBe('0.393 ms')
  })

  it('formats nodes as text lines', () => {
    const { nodes } = samplePlan()
    const scan = nodes[0].children[0].children[0]
    expect(nodeLabel(scan)).toBe('Seq Scan on numbers n (n < 500)')
    expect(nodeNumbers(scan)).toBe('est=5, actual=499 × 1, cost=18.5, time=6 ms')
    expect(nodeLine(scan)).toBe('-> Seq Scan on numbers n (n < 500)  (est=5, actual=499 × 1, cost=18.5, time=6 ms)')
    expect(nodeLine(planNode('SCAN', { object: 't' }))).toBe('-> SCAN on t')
    expect(nodeNumbers(planNode('x', { actualRows: 2 }))).toBe('actual=2')
    expect(nodeNumbers(planNode('x', { loops: 0, estRows: 3 }))).toBe('est=3, never executed')
  })

  it('copies a subtree and the whole plan as indented text', () => {
    const plan = samplePlan()
    const hash = plan.nodes[0].children[0].children[1]
    expect(nodeToText(hash)).toBe('-> Hash  (est=3, actual=3 × 1, cost=1.03, time=0.02 ms)\n    -> Seq Scan on items i  (est=3, actual=3 × 1, cost=1.03, time=0.01 ms)')
    const text = planToText(plan)
    const lines = text.split('\n')
    expect(lines[0]).toBe('-> Sort n.label  (est=150, actual=150 × 1, cost=27.23, time=10 ms)')
    expect(lines[2]).toMatch(/^ {8}-> Seq Scan on numbers n/)
    expect(lines.slice(-2)).toEqual(['Planning time: 0.5 ms', 'Execution time: 10 ms'])
    expect(planToText(sqlitePlan())).toBe('-> SCAN on n\n-> CORRELATED SCALAR SUBQUERY 1\n    -> SEARCH on i USING INTEGER PRIMARY KEY (rowid=?)')
  })

  it('copies the plan as a Markdown table', () => {
    const plan = samplePlan()
    plan.nodes[0].detail = 'a | b'
    const md = planToMarkdown(plan).split('\n')
    expect(md[0]).toBe('| Operation | Object | Detail | Est. rows | Actual rows | Loops | Cost | Time (ms) | Self % |')
    expect(md[1]).toBe('| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |')
    expect(md[2]).toBe('| Sort |  | a \\| b | 150 | 150 | 1 | 27.23 | 10 | 30.0% |')
    expect(md[4]).toBe('|   └ Seq Scan | numbers n | (n < 500) | 5 | 499 | 1 | 18.5 | 6 | 60.0% |')
    expect(md[md.length - 1]).toBe('Planning time: 0.5 ms · Execution time: 10 ms')
    const lite = planToMarkdown(sqlitePlan()).split('\n')
    expect(lite).toHaveLength(5)
    expect(lite[2]).toBe('| SCAN | n |  |  |  |  |  |  |  |')
  })

  it('copies properties as TSV', () => {
    const node = planNode('x', { props: [{ key: 'Filter', value: 'a\tb\nc' }, { key: 'Rows', value: '3' }] })
    expect(nodeProps(node)).toBe('Filter\ta b c\nRows\t3')
    expect(nodeProps(planNode('x'))).toBe('')
  })
})
