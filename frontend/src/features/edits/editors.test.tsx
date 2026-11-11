import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ResultColumn } from '../workspace/types'
import { CellEditor, editorKind, initialText, isMultiline, toEditValue, validateText, type EditorHandle } from './editors'

const col = (type: string, over: Partial<ResultColumn> = {}): ResultColumn => ({ name: 'c', type, isPK: false, nullable: true, ...over })

afterEach(cleanup)

describe('editor helpers', () => {
  it('picks an editor per column type', () => {
    const cases: [string, string, string][] = [
      ['integer', 'postgres', 'integer'],
      ['bigint unsigned', 'mysql', 'integer'],
      ['numeric(12,4)', 'postgres', 'number'],
      ['double precision', 'postgres', 'number'],
      ['boolean', 'postgres', 'bool'],
      ['tinyint(1)', 'mysql', 'bool'],
      ['tinyint(1)', 'postgres', 'integer'],
      ['date', 'postgres', 'date'],
      ['timestamp with time zone', 'postgres', 'datetime'],
      ['DATETIME(6)', 'mysql', 'datetime'],
      ['time without time zone', 'postgres', 'time'],
      ['jsonb', 'postgres', 'json'],
      ['bytea', 'postgres', 'bytes'],
      ['BLOB', 'sqlite', 'bytes'],
      ['interval', 'postgres', 'text'],
      ['integer[]', 'postgres', 'text'],
      ['_int4', 'postgres', 'text'],
      ['character varying(20)', 'postgres', 'text'],
    ]
    for (const [type, engine, want] of cases) expect(editorKind(col(type), engine as 'postgres'), type).toBe(want)
    expect(editorKind(col('mood', { enumValues: ['a', 'b'] }), 'postgres')).toBe('enum')
    expect(editorKind(col('mood', { enumValues: [] }), 'postgres')).toBe('text')
  })

  it('validates typed text', () => {
    expect(validateText('integer', ' -42 ')).toBeNull()
    expect(validateText('integer', '4.2')).toMatch(/whole/)
    expect(validateText('number', '1.5e3')).toBeNull()
    expect(validateText('number', 'NaN')).toBeNull()
    expect(validateText('number', '1,5')).toMatch(/number/)
    expect(validateText('date', '2024-02-29')).toBeNull()
    expect(validateText('date', '29/02/2024')).toMatch(/YYYY/)
    expect(validateText('datetime', '2024-01-02T03:04:05.123456+02:00')).toBeNull()
    expect(validateText('datetime', '2024-01-02 03:04')).toBeNull()
    expect(validateText('datetime', 'yesterday')).toMatch(/YYYY/)
    expect(validateText('time', '12:30:00')).toBeNull()
    expect(validateText('time', 'noon')).toMatch(/HH/)
    expect(validateText('bool', 'TRUE')).toBeNull()
    expect(validateText('bool', 'yes')).toMatch(/true/)
    expect(validateText('bytes', '0x00 ff')).toBeNull()
    expect(validateText('bytes', 'abc')).toMatch(/hex/)
    expect(validateText('json', '{"a": 1}')).toBeNull()
    expect(validateText('json', '{a}')).toMatch(/JSON/)
    expect(validateText('text', 'anything')).toBeNull()
  })

  it('converts accepted text', () => {
    expect(toEditValue('bytes', '0xAB cd')).toEqual({ kind: 'bytes', hex: 'abcd' })
    expect(toEditValue('bool', ' TRUE ')).toEqual({ kind: 'value', text: 'true' })
    expect(toEditValue('json', ' [] ')).toEqual({ kind: 'value', text: ' [] ' })
    expect(toEditValue('text', ' keep ')).toEqual({ kind: 'value', text: ' keep ' })
    expect(toEditValue('integer', ' 7 ')).toEqual({ kind: 'value', text: '7' })
    expect(initialText({ kind: 'value', text: 'x' }, { k: 'str', v: 'y' })).toBe('x')
    expect(initialText({ kind: 'bytes', hex: 'ff' }, undefined)).toBe('ff')
    expect(initialText({ kind: 'null' }, { k: 'str', v: 'y' })).toBe('')
    expect(initialText(undefined, { k: 'num', v: 3 })).toBe('3')
    expect(isMultiline('json', '1')).toBe(true)
    expect(isMultiline('text', 'a\nb')).toBe(true)
    expect(isMultiline('text', 'x'.repeat(121))).toBe(true)
    expect(isMultiline('text', 'short')).toBe(false)
    expect(isMultiline('integer', 'a\nb')).toBe(false)
  })
})

describe('CellEditor', () => {
  const setup = (over: Partial<Parameters<typeof CellEditor>[0]> = {}) => {
    const props = { column: col('text'), engine: 'postgres' as const, text: 'old', onCommit: vi.fn(), onCancel: vi.fn(), ...over }
    render(<CellEditor {...props} />)
    return props
  }

  it('edits text: Enter saves, Escape cancels', async () => {
    const user = userEvent.setup()
    const p = setup()
    const input = screen.getByRole('textbox', { name: 'Edit c' })
    expect(input).toHaveFocus()
    await user.clear(input)
    await user.type(input, 'new{Enter}')
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: 'new' })
    // A second finish is ignored.
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(p.onCancel).not.toHaveBeenCalled()
    cleanup()
    const q = setup()
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' })
    expect(q.onCancel).toHaveBeenCalled()
  })

  it('starts with a typed character and offers NULL and DEFAULT', async () => {
    const user = userEvent.setup()
    const p = setup({ typed: 'x' })
    expect(screen.getByRole('textbox')).toHaveValue('x')
    await user.click(screen.getByRole('button', { name: 'NULL' }))
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'null' })
    cleanup()
    const q = setup({ column: col('text', { nullable: false }), allowDefault: true })
    expect(screen.queryByRole('button', { name: 'NULL' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Default' }))
    expect(q.onCommit).toHaveBeenCalledWith({ kind: 'default' })
    cleanup()
    setup({ allowDefault: false })
    expect(screen.queryByRole('button', { name: 'Default' })).toBeNull()
  })

  it('validates numbers and blocks saving', async () => {
    const user = userEvent.setup()
    const p = setup({ column: col('integer'), text: '1' })
    const input = screen.getByRole('textbox')
    await user.type(input, 'x')
    expect(screen.getByRole('alert')).toHaveTextContent('whole number')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.keyboard('{Enter}')
    expect(p.onCommit).not.toHaveBeenCalled()
    await user.type(input, '{Backspace}2')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: '12' })
  })

  it('uses a select for enums and booleans', async () => {
    const user = userEvent.setup()
    const p = setup({ column: col('mood', { enumValues: ['happy', 'sad'] }), text: 'odd' })
    const sel = screen.getByRole('combobox')
    expect([...sel.querySelectorAll('option')].map((o) => o.value)).toEqual(['odd', 'happy', 'sad'])
    await user.selectOptions(sel, 'sad')
    await user.keyboard('{Enter}')
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: 'sad' })
    cleanup()
    const q = setup({ column: col('boolean'), text: '' })
    expect(screen.getByRole('combobox')).toHaveValue('true')
    await user.selectOptions(screen.getByRole('combobox'), 'false')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(q.onCommit).toHaveBeenCalledWith({ kind: 'value', text: 'false' })
  })

  it('uses a textarea for JSON and long text (Mod+Enter saves)', async () => {
    const user = userEvent.setup()
    const p = setup({ column: col('jsonb'), text: '{"a":1}' })
    const ta = screen.getByRole('textbox')
    expect(ta.tagName).toBe('TEXTAREA')
    await user.type(ta, '{Enter}')
    expect(p.onCommit).not.toHaveBeenCalled() // newline in a textarea
    fireEvent.keyDown(ta, { key: 'Enter', ctrlKey: true })
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: '{"a":1}\n' })
    cleanup()
    setup()
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    expect(screen.getByRole('textbox').tagName).toBe('TEXTAREA')
    cleanup()
    setup({ text: '' })
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'y'.repeat(130) } })
    expect(screen.getByRole('textbox').tagName).toBe('TEXTAREA')
  })

  it('commits on an outside click, or cancels when invalid', () => {
    const p = setup({ commitOnOutsideClick: true })
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'z' } })
    fireEvent.mouseDown(screen.getByRole('textbox'))
    expect(p.onCommit).not.toHaveBeenCalled()
    fireEvent.mouseDown(document.body)
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: 'z' })
    cleanup()
    const q = setup({ commitOnOutsideClick: true, column: col('date'), text: 'bad' })
    fireEvent.mouseDown(document.body)
    expect(q.onCancel).toHaveBeenCalled()
  })

  it('exposes a handle for keys that reached the grid', () => {
    vi.useFakeTimers()
    const handle = createRef<EditorHandle | null>() as { current: EditorHandle | null }
    const p = setup({ handle, typed: 'a' })
    act(() => vi.advanceTimersByTime(50))
    act(() => handle.current?.type('b'))
    expect(screen.getByRole('textbox')).toHaveValue('ab')
    act(() => handle.current?.commit())
    expect(p.onCommit).toHaveBeenCalledWith({ kind: 'value', text: 'ab' })
    cleanup()
    expect(handle.current).toBeNull()
    const q = setup({ handle })
    act(() => handle.current?.cancel())
    expect(q.onCancel).toHaveBeenCalled()
    vi.useRealTimers()
  })
})
