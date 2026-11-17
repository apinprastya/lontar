import { undo } from '@codemirror/commands'
import { EditorView } from '@codemirror/view'
import { act, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import SqlEditor, { applyRequest, type SqlEditorProps } from './SqlEditor'

function setup(over: Partial<SqlEditorProps> = {}) {
  const props: SqlEditorProps = {
    value: 'SELECT 1;\nSELECT 2;',
    kind: 'postgres',
    completion: null,
    current: null,
    request: null,
    onChange: vi.fn(),
    onSelection: vi.fn(),
    onRun: vi.fn(),
    onCancel: vi.fn(() => false),
    onNeedColumns: vi.fn(),
    ...over,
  }
  const utils = render(<SqlEditor {...props} />)
  const view = () => EditorView.findFromDOM(utils.container.querySelector('.cm-editor') as HTMLElement)!
  return { props, view, ...utils }
}

describe('SqlEditor', () => {
  it('mounts CodeMirror with the text and reports edits and selection', () => {
    const { props, view } = setup({ initialCursor: 3 })
    expect(view().state.doc.toString()).toBe('SELECT 1;\nSELECT 2;')
    expect(view().state.selection.main.head).toBe(3)
    act(() => view().dispatch({ changes: { from: 0, insert: '-- c\n' } }))
    expect(props.onChange).toHaveBeenCalledWith('-- c\nSELECT 1;\nSELECT 2;')
    act(() => view().dispatch({ selection: { anchor: 5, head: 11 } }))
    expect(props.onSelection).toHaveBeenLastCalledWith(11, { from: 5, to: 11 })
    act(() => view().dispatch({ selection: { anchor: 2 } }))
    expect(props.onSelection).toHaveBeenLastCalledWith(2, null)
  })

  it('follows props: text, statement highlight, completion and requests', () => {
    const { props, view, rerender, container } = setup()
    const rr = (over: Partial<SqlEditorProps>) => rerender(<SqlEditor {...props} {...over} />)
    rr({ value: 'SELECT 3' })
    expect(view().state.doc.toString()).toBe('SELECT 3')
    rr({ value: 'SELECT 3', current: { from: 0, to: 8 } })
    expect(container.querySelector('.cm-current-statement')?.textContent).toBe('SELECT 3')
    rr({
      value: 'SELECT 3',
      kind: 'sqlite',
      completion: { database: 'main', schema: 'main', schemas: ['main'], tables: [], columnsLoaded: true },
    })
    rr({ value: 'SELECT 3', request: { seq: 1, type: 'select', from: 7, to: 8 } })
    expect(view().state.selection.main).toMatchObject({ from: 7, to: 8 })
    // The same request is applied once.
    act(() => view().dispatch({ selection: { anchor: 0 } }))
    rr({ value: 'SELECT 3', request: { seq: 1, type: 'select', from: 7, to: 8 } })
    expect(view().state.selection.main.from).toBe(0)
    rr({ value: 'SELECT 3', request: { seq: 2, type: 'insert', text: ' + 1' } })
    expect(props.onChange).toHaveBeenLastCalledWith(' + 1SELECT 3')
  })

  it('ignores a request made before it mounted', () => {
    const { view } = setup({ request: { seq: 5, type: 'insert', text: 'X' } })
    expect(view().state.doc.toString()).toBe('SELECT 1;\nSELECT 2;')
  })

  it('applies requests to a view', () => {
    const { view } = setup()
    const v = view()
    act(() => applyRequest(v, { seq: 1, type: 'error', pos: 17, from: 10, to: 19 }))
    expect(v.state.selection.main.head).toBe(17)
    expect(v.dom.querySelector('.cm-sql-error')?.textContent).toBe('2')
    act(() => applyRequest(v, { seq: 2, type: 'select', from: -5, to: 999 }))
    expect(v.state.selection.main).toMatchObject({ from: 0, to: v.state.doc.length })
    act(() => applyRequest(v, { seq: 3, type: 'search' }))
    expect(v.dom.querySelector('.cm-search')).not.toBeNull()
  })

  it('wires the run keys to the props', () => {
    const onRun = vi.fn()
    const { view } = setup({ onRun })
    const v = view()
    act(() => {
      v.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }))
    })
    expect(onRun).toHaveBeenCalledWith('current')
  })

  it('formats the whole text in one undoable change, keeping the cursor', () => {
    const { view, props } = setup({ value: 'select a from t; select 2' })
    const v = view()
    // The cursor after "select a" (7 non-blank characters).
    act(() => v.dispatch({ selection: { anchor: 8 } }))
    const text = 'SELECT\n  a\nFROM\n  t;\n\nSELECT\n  2'
    act(() => applyRequest(v, { seq: 1, type: 'replace', from: 0, to: 25, expect: 'select a from t; select 2', text, select: false }))
    expect(v.state.doc.toString()).toBe(text)
    expect(v.state.selection.main.head).toBe(text.indexOf('a') + 1)
    expect(props.onChange).toHaveBeenLastCalledWith(text)
    act(() => {
      undo(v)
    })
    expect(v.state.doc.toString()).toBe('select a from t; select 2')
  })

  it('formats a selection and selects the result; cursors outside it move along', () => {
    const { view } = setup({ value: 'x; select 1; y' })
    const v = view()
    act(() => applyRequest(v, { seq: 1, type: 'replace', from: 3, to: 11, expect: 'select 1', text: 'SELECT\n  1', select: true }))
    expect(v.state.doc.toString()).toBe('x; SELECT\n  1; y')
    expect(v.state.selection.main).toMatchObject({ from: 3, to: 13 })
    act(() => v.dispatch({ selection: { anchor: v.state.doc.length } }))
    act(() => applyRequest(v, { seq: 2, type: 'replace', from: 3, to: 13, expect: 'SELECT\n  1', text: 'select 1', select: false }))
    expect(v.state.selection.main.head).toBe(v.state.doc.length)
    act(() => v.dispatch({ selection: { anchor: 1 } }))
    act(() => applyRequest(v, { seq: 3, type: 'replace', from: 3, to: 11, expect: 'select 1', text: 'SELECT 1', select: false }))
    expect(v.state.selection.main.head).toBe(1)
    expect(v.state.doc.toString()).toBe('x; SELECT 1; y')
  })

  it('drops a replace request when the text changed meanwhile', () => {
    const { view } = setup({ value: 'select 1' })
    const v = view()
    act(() => applyRequest(v, { seq: 1, type: 'replace', from: 0, to: 8, expect: 'select 2', text: 'SELECT 2', select: false }))
    act(() => applyRequest(v, { seq: 2, type: 'replace', from: 0, to: 80, expect: 'select 1', text: 'SELECT 1', select: false }))
    expect(v.state.doc.toString()).toBe('select 1')
  })

  it('wires Mod-Shift-F to onFormat', () => {
    const onFormat = vi.fn()
    const { view } = setup({ onFormat })
    act(() => {
      view().contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'F', keyCode: 70, ctrlKey: true, shiftKey: true, bubbles: true }))
    })
    expect(onFormat).toHaveBeenCalled()
    // Without a handler the key is still consumed.
    const other = setup()
    act(() => {
      other.view().contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'F', keyCode: 70, ctrlKey: true, shiftKey: true, bubbles: true }))
    })
  })
})
