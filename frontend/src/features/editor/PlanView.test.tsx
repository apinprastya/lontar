import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../../lib/clipboard'
import { planNode, samplePlan } from '../../test/fakeEditorApi'
import { nodeToText, planToMarkdown, planToText } from './plan'
import PlanView from './PlanView'
import type { Plan } from './types'

const copied = vi.mocked(copyText)

function show(plan: Plan = samplePlan()) {
  return render(<PlanView state={{ plan, analyze: plan.analyzed, sql: 'SELECT 1' }} platform="linux" />)
}

const rowNames = () => screen.getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('gridcell')[0].textContent)

describe('PlanView', () => {
  beforeEach(() => copied.mockClear())

  it('renders the plan as a tree table with a summary', () => {
    show()
    expect(screen.getByText('PostgreSQL')).toBeInTheDocument()
    expect(screen.getByText('Analyzed')).toBeInTheDocument()
    expect(screen.getByText(/Planning 0.5 ms · Execution 10 ms · ~150 rows estimated · 150 rows/)).toBeInTheDocument()
    const grid = screen.getByRole('treegrid', { name: 'Query plan' })
    const headers = within(grid).getAllByRole('columnheader').map((h) => h.textContent)
    expect(headers).toEqual(['Operation', 'Object', 'Est. rows', 'Actual rows × loops', 'Cost', 'Time (ms)', 'Self'])
    const rows = within(grid).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(5)
    expect(rows[2]).toHaveAttribute('aria-level', '3')
    expect(rows[0]).toHaveAttribute('aria-expanded', 'true')
    expect(rows[4]).not.toHaveAttribute('aria-expanded')
    expect(rows[0]).toHaveAttribute('aria-selected', 'true')
    // Hot nodes are tinted; misestimates are flagged.
    expect(rows[2]).toHaveClass('hot')
    expect(rows[1]).not.toHaveClass('hot')
    expect(screen.getByLabelText('Estimated 5 rows, actual 499 (100× off)')).toBeInTheDocument()
    expect(within(rows[2]).getByText('60.0%')).toBeInTheDocument()
  })

  it('expands, collapses and selects nodes', () => {
    show()
    fireEvent.click(screen.getByRole('button', { name: 'Collapse Hash Join' }))
    expect(rowNames()).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: 'Expand Hash Join' }))
    expect(rowNames()).toHaveLength(5)
    fireEvent.click(screen.getByRole('button', { name: 'Collapse all' }))
    expect(rowNames()).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Expand all' }))
    expect(rowNames()).toHaveLength(5)

    // Selecting a row shows its properties.
    fireEvent.click(screen.getAllByRole('row')[3])
    const props = screen.getByRole('complementary', { name: 'Node properties' })
    expect(within(props).getByText('Filter')).toBeInTheDocument()
    expect(within(props).getAllByText('(n < 500)').length).toBeGreaterThan(0)
    fireEvent.click(screen.getAllByRole('row')[2])
    expect(within(screen.getByRole('complementary')).getByText('No properties.')).toBeInTheDocument()
  })

  it('moves through the tree with the keyboard and copies the subtree with Mod+C', async () => {
    show()
    const grid = screen.getByRole('treegrid')
    const selected = () => screen.getAllByRole('row').find((r) => r.getAttribute('aria-selected') === 'true')
    fireEvent.keyDown(grid, { key: 'ArrowDown' })
    expect(selected()).toHaveTextContent('Hash Join')
    fireEvent.keyDown(grid, { key: 'ArrowLeft' })
    expect(rowNames()).toHaveLength(2)
    fireEvent.keyDown(grid, { key: 'ArrowRight' })
    expect(rowNames()).toHaveLength(5)
    fireEvent.keyDown(grid, { key: 'ArrowUp' })
    expect(selected()).toHaveTextContent('Sort')
    fireEvent.keyDown(grid, { key: 'ArrowDown' })
    fireEvent.keyDown(grid, { key: 'c', ctrlKey: true })
    const plan = samplePlan()
    await waitFor(() => expect(copied).toHaveBeenCalledWith(nodeToText(plan.nodes[0].children[0])))
    expect(screen.getByRole('status')).toHaveTextContent('Copied subtree')
    // Other keys are left alone.
    fireEvent.keyDown(grid, { key: 'x' })
    expect(copied).toHaveBeenCalledTimes(1)
  })

  it('copies from the split button and its menu', async () => {
    const plan = samplePlan()
    show(plan)
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(copied).toHaveBeenLastCalledWith(planToText(plan)))
    expect(screen.getByRole('status')).toHaveTextContent('Copied')

    const pick = async (label: string, want: string) => {
      fireEvent.click(screen.getByRole('button', { name: 'More copy options' }))
      fireEvent.click(within(screen.getByRole('menu', { name: 'Copy options' })).getByRole('menuitem', { name: label }))
      await waitFor(() => expect(copied).toHaveBeenLastCalledWith(want))
      expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    }
    await pick('Copy as text', planToText(plan))
    await pick('Copy raw', plan.raw)
    await pick('Copy as Markdown table', planToMarkdown(plan))
    await pick('Copy query with EXPLAIN', plan.sql)

    // The menu closes with Escape and on an outside click.
    fireEvent.click(screen.getByRole('button', { name: 'More copy options' }))
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'More copy options' }))
    fireEvent.mouseDown(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('copies a node, its subtree and its properties from the context menu', async () => {
    const plan = samplePlan()
    show(plan)
    const scan = plan.nodes[0].children[0].children[0]
    const open = () => fireEvent.contextMenu(screen.getAllByRole('row')[3], { clientX: 10, clientY: 20 })
    open()
    expect(screen.getAllByRole('row')[3]).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy node' }))
    await waitFor(() => expect(copied).toHaveBeenLastCalledWith('-> Seq Scan on numbers n (n < 500)  (est=5, actual=499 × 1, cost=18.5, time=6 ms)'))
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: /Copy subtree/ }))
    await waitFor(() => expect(copied).toHaveBeenLastCalledWith(nodeToText(scan)))
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy properties' }))
    await waitFor(() => expect(copied).toHaveBeenLastCalledWith('Node Type\tSeq Scan\nFilter\t(n < 500)'))

    // Escape, the backdrop and a second right-click close it.
    open()
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Seq Scan actions' }), { key: 'Escape' })
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    open()
    fireEvent.mouseDown(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    open()
    fireEvent.contextMenu(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    // A node without properties cannot copy them.
    fireEvent.contextMenu(screen.getAllByRole('row')[2])
    expect(screen.getByRole('menuitem', { name: 'Copy properties' })).toBeDisabled()
  })

  it('shows the raw output with its own copy button', async () => {
    const plan = samplePlan()
    show(plan)
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }))
    expect(screen.getByRole('button', { name: 'Raw' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Raw plan')).toHaveTextContent(plan.raw)
    expect(screen.queryByRole('treegrid')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Copy raw' }))
    await waitFor(() => expect(copied).toHaveBeenLastCalledWith(plan.raw))
    fireEvent.click(screen.getByRole('button', { name: 'Tree' }))
    expect(screen.getByRole('treegrid')).toBeInTheDocument()
  })

  it('reports a failed copy', async () => {
    copied.mockRejectedValueOnce(new Error('no clipboard'))
    show()
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Copy failed: no clipboard'))
  })

  it('hides the numbers an engine does not report (SQLite)', () => {
    show(
      samplePlan({
        engine: 'sqlite',
        analyzed: false,
        planningMs: null,
        executionMs: null,
        nodes: [planNode('SCAN', { object: 'n' }), planNode('SEARCH', { object: 'i', loops: 0 })],
      }),
    )
    expect(screen.getByText('SQLite')).toBeInTheDocument()
    expect(screen.queryByText('Analyzed')).not.toBeInTheDocument()
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual(['Operation', 'Object', 'Actual rows × loops'])
    expect(screen.getByText('never executed')).toBeInTheDocument()
  })

  it('shows errors, cancels, notices and empty plans', () => {
    const { unmount } = show(
      samplePlan({ nodes: [], engine: 'other', error: { message: 'syntax error', code: '42601', detail: 'more', hint: 'try', position: 3 }, notices: ['The session was reset.'] }),
    )
    expect(screen.getByText('other')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('syntax error more Hint: try')
    expect(screen.getByText('The session was reset.')).toBeInTheDocument()
    expect(screen.queryByText('No plan.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Collapse all' })).not.toBeInTheDocument()
    unmount()
    const r = show(samplePlan({ nodes: [], cancelled: true, error: { message: 'Query cancelled', code: '', position: null } }))
    expect(screen.getByRole('alert')).toHaveTextContent('Query cancelled.')
    r.unmount()
    show(samplePlan({ nodes: [] }))
    expect(screen.getByText('No plan.')).toBeInTheDocument()
  })

  it('resets selection and collapsed nodes for a new plan', () => {
    const plan = samplePlan()
    const { rerender } = show(plan)
    fireEvent.click(screen.getByRole('button', { name: 'Collapse all' }))
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    const next = samplePlan()
    rerender(<PlanView state={{ plan: next, analyze: true, sql: 'SELECT 2' }} platform="linux" />)
    expect(rowNames()).toHaveLength(5)
    expect(screen.getAllByRole('row')[1]).toHaveAttribute('aria-selected', 'true')
  })
})
