import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { listeners } = vi.hoisted(() => ({ listeners: new Map<string, (d: unknown) => void>() }))
vi.mock('../../lib/events', () => ({
  onEvent: (name: string, cb: (d: unknown) => void) => {
    listeners.set(name, cb)
    return () => listeners.delete(name)
  },
}))
vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))

import { DEFAULT_SETTINGS } from '../settings/types'
import { useSettings } from '../../stores/settings'
import { DialogService, ExportService, resetBindings } from '../../test/mockBindings'
import type { FilesApi } from './api'
import { filesApi } from './api'
import ExportDialog, { PROGRESS_EVENT, safeFileName, type ExportSource } from './ExportDialog'

function fakeFiles(over: Partial<FilesApi> = {}): FilesApi {
  return {
    export: vi.fn(async (req) => ({ rows: 42, path: req.path })),
    cancelExport: vi.fn(async () => true),
    checkQuery: vi.fn(async () => {}),
    saveText: vi.fn(async () => {}),
    previewImport: vi.fn(),
    runImport: vi.fn(),
    cancelImport: vi.fn(),
    ...over,
  }
}

const tableSource: ExportSource = {
  type: 'table',
  schema: 'public',
  table: 'users',
  request: { schema: 'public', table: 'users', filters: [{ column: 'id', op: '>', value: '5', value2: '' }], rawWhere: '', sort: [{ column: 'id', desc: true }], offset: 300, limit: 300 },
}
const cols = [
  { name: 'id', type: 'int4', isPK: true, nullable: false },
  { name: 'note', type: 'text', isPK: false, nullable: true },
]

describe('ExportDialog', () => {
  beforeEach(() => {
    resetBindings()
    listeners.clear()
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, csvExport: { ...DEFAULT_SETTINGS.csvExport, delimiter: ';' } } })
  })
  afterEach(cleanup)

  it('exports a whole table as CSV with the settings defaults, progress and the result', async () => {
    let finish: (v: { rows: number; path: string }) => void = () => {}
    const api = fakeFiles({ export: vi.fn(() => new Promise<{ rows: number; path: string }>((r) => (finish = r))) })
    const pickSave = vi.fn(async () => '/tmp/users.csv')
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(<ExportDialog sessionId="s1" source={tableSource} onClose={onClose} api={api} pickSave={pickSave} />)
    expect(screen.getByText(/All rows of public.users matching the filters, in the current sort order/)).toBeInTheDocument()
    expect(screen.getByLabelText('Delimiter')).toHaveValue(';')
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(screen.getByText('Choose where to save the file.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(pickSave).toHaveBeenCalledWith('Export users', 'users.csv')
    await user.selectOptions(screen.getByLabelText('NULL as'), '\\N')
    await user.selectOptions(screen.getByLabelText('Line endings'), 'crlf')
    await user.selectOptions(screen.getByLabelText('Quote'), "'")
    await user.click(screen.getByRole('checkbox', { name: 'UTF-8 BOM (for Excel)' }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(api.export).toHaveBeenCalledWith(
      expect.objectContaining({
        sessionId: 's1',
        path: '/tmp/users.csv',
        source: 'table',
        table: { ...tableSource.request, offset: 0, limit: 0 },
        options: expect.objectContaining({ format: 'csv', csv: { delimiter: ';', header: true, quote: "'", null: '\\N', nullText: '', crlf: true, bom: true } }),
      }),
    )
    const jobId = (api.export as ReturnType<typeof vi.fn>).mock.calls[0][0].jobId
    act(() => listeners.get(PROGRESS_EVENT)!({ jobId, rows: 1500 }))
    expect(screen.getByText(/1,500 rows written/)).toBeInTheDocument()
    act(() => listeners.get(PROGRESS_EVENT)!({ jobId: 'other', rows: 9 }))
    expect(screen.getByText(/1,500 rows written/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel export' }))
    expect(api.cancelExport).toHaveBeenCalledWith(jobId)
    await act(async () => finish({ rows: 2000, path: '/tmp/users.csv' }))
    expect(screen.getByText(/Exported 2,000 rows to/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('exports JSON and SQL with their options; shows errors', async () => {
    const api = fakeFiles()
    const user = userEvent.setup()
    render(<ExportDialog sessionId="s1" source={tableSource} onClose={vi.fn()} api={api} />)
    await user.type(screen.getByLabelText('Save to'), '/tmp/u.json')
    await user.click(screen.getByRole('radio', { name: 'JSON' }))
    await user.selectOptions(screen.getByLabelText('Layout'), 'ndjson')
    await user.selectOptions(screen.getByLabelText('Binary values'), 'base64')
    await user.click(screen.getByRole('checkbox', { name: /All numbers as/ }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(api.export).toHaveBeenLastCalledWith(expect.objectContaining({ options: expect.objectContaining({ format: 'json', json: { ndjson: true, numbersAsStrings: true, bytes: 'base64' } }) }))
    cleanup()

    const failing = fakeFiles({ export: vi.fn(async () => Promise.reject(new Error('permission denied'))) })
    render(<ExportDialog sessionId="s1" source={tableSource} onClose={vi.fn()} api={failing} />)
    await user.type(screen.getByLabelText('Save to'), '/tmp/u.sql')
    await user.click(screen.getByRole('radio', { name: 'SQL INSERT' }))
    const target = screen.getByLabelText('Insert into')
    expect(target).toHaveValue('users')
    await user.clear(target)
    await user.type(target, 'users_copy')
    const batch = screen.getByLabelText('Rows per INSERT')
    await user.clear(batch)
    await user.type(batch, '500')
    await user.click(screen.getByRole('checkbox', { name: 'Add CREATE TABLE' }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(failing.export).toHaveBeenCalledWith(expect.objectContaining({ options: expect.objectContaining({ format: 'sql', sql: { table: 'users_copy', schema: '', batchSize: 500, createTable: true } }) }))
    expect(await screen.findByRole('alert')).toHaveTextContent('permission denied')
  })

  it('exports loaded query rows, or all rows after a warning with the statement', async () => {
    const api = fakeFiles()
    const user = userEvent.setup()
    const source: ExportSource = { type: 'query', sql: 'SELECT id, note FROM t', columns: cols, rows: [[{ k: 'num', v: 1 }, { k: 'str', v: 'x', trunc: true }]], title: 'Query 1' }
    render(<ExportDialog sessionId="s1" source={source} onClose={vi.fn()} api={api} />)
    await waitFor(() => expect(screen.getByRole('radio', { name: /All rows/ })).toBeEnabled())
    expect(screen.getByText(/cut for display; choose All rows/)).toBeInTheDocument()
    await user.type(screen.getByLabelText('Save to'), '/tmp/q.csv')
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(api.export).toHaveBeenLastCalledWith(expect.objectContaining({ source: 'rows', columns: [{ name: 'id', type: 'int4' }, { name: 'note', type: 'text' }], rows: source.rows }))
    cleanup()

    render(<ExportDialog sessionId="s1" source={source} onClose={vi.fn()} api={api} />)
    await waitFor(() => expect(screen.getByRole('radio', { name: /All rows/ })).toBeEnabled())
    await user.click(screen.getByRole('radio', { name: /All rows/ }))
    expect(screen.getByRole('note')).toHaveTextContent('SELECT id, note FROM t')
    await user.type(screen.getByLabelText('Save to'), '/tmp/q.csv')
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(api.export).toHaveBeenLastCalledWith(expect.objectContaining({ source: 'query', sql: 'SELECT id, note FROM t' }))
  })

  it('does not offer re-running a write', async () => {
    const api = fakeFiles({ checkQuery: vi.fn(async () => Promise.reject(new Error('only a single read-only statement that returns rows can be run again for export'))) })
    render(<ExportDialog sessionId="s1" source={{ type: 'query', sql: 'DELETE FROM t RETURNING *', columns: cols, rows: [], title: 'Q' }} onClose={vi.fn()} api={api} />)
    expect(await screen.findByText(/only a single read-only statement/)).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /All rows/ })).toBeDisabled()
  })

  it('exports a grid selection as TSV or CSV text', async () => {
    const api = fakeFiles()
    const user = userEvent.setup()
    const source: ExportSource = {
      type: 'selection',
      columns: cols,
      rows: [
        [{ k: 'num', v: 1 }, { k: 'str', v: 'a\tb' }],
        [{ k: 'num', v: 2 }, { k: 'null' }],
      ],
      spec: { rows: [0, 1] },
      title: 'users',
    }
    render(<ExportDialog sessionId="s1" source={source} onClose={vi.fn()} api={api} pickSave={async () => Promise.reject(new Error('no dialogs'))} />)
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(await screen.findByText('no dialogs')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Save to'), '/tmp/sel.tsv')
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(api.saveText).toHaveBeenCalledWith('/tmp/sel.tsv', 'id\tnote\n1\t"a\tb"\n2\t\n')
    expect(await screen.findByText(/Exported 2 rows/)).toBeInTheDocument()
    cleanup()

    render(<ExportDialog sessionId="s1" source={source} onClose={vi.fn()} api={api} />)
    await user.click(screen.getByRole('radio', { name: 'CSV' }))
    await user.selectOptions(screen.getByLabelText('NULL as'), 'custom')
    await user.type(screen.getByLabelText('NULL text'), 'NULL')
    await user.click(screen.getByRole('checkbox', { name: 'Header row' }))
    await user.type(screen.getByLabelText('Save to'), '/tmp/sel.csv')
    await user.keyboard('{Enter}')
    expect(api.saveText).toHaveBeenLastCalledWith('/tmp/sel.csv', '1;a\tb\n2;NULL\n')
  })

  it('makes safe file names and wraps the bindings', async () => {
    expect(safeFileName('Query 1: users/2024', 'csv')).toBe('Query_1_users_2024.csv')
    expect(safeFileName('***', 'json')).toBe('export.json')
    ExportService.Export.mockResolvedValue({ rows: 3, path: '/x' })
    expect(await filesApi.export({ jobId: 'j', sessionId: 's', path: '/x', options: {} as never, source: 'rows' })).toEqual({ rows: 3, path: '/x' })
    ExportService.Export.mockResolvedValue(null)
    expect(await filesApi.export({ jobId: 'j', sessionId: 's', path: '/y', options: {} as never, source: 'rows' })).toEqual({ rows: 0, path: '/y' })
    await filesApi.cancelExport('j')
    expect(ExportService.Cancel).toHaveBeenCalledWith('j')
    await filesApi.checkQuery('s', 'SELECT 1')
    await filesApi.saveText('/p', 't')
    expect(ExportService.SaveText).toHaveBeenCalledWith('/p', 't')
    DialogService.SaveFile.mockResolvedValue('/chosen.csv')
    const { dialogApi } = await import('../connections/dialogApi')
    expect(await dialogApi.saveFile('t', 'f.csv')).toBe('/chosen.csv')
    DialogService.OpenFile.mockResolvedValue(null as unknown as string)
    expect(await dialogApi.openFile('t')).toBe('')
  })
})
