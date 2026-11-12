import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { listeners } = vi.hoisted(() => ({ listeners: new Map<string, (d: unknown) => void>() }))
vi.mock('../../lib/events', () => ({
  onEvent: (name: string, cb: (d: unknown) => void) => {
    listeners.set(name, cb)
    return () => listeners.delete(name)
  },
}))
vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { ImportService, resetBindings } from '../../test/mockBindings'
import { filesApi, type FilesApi, type ImportPreview, type ImportResult } from '../export/api'
import ImportWizard, { IMPORT_PROGRESS_EVENT, mappingByColumn, type ImportWizardProps } from './ImportWizard'

const preview: ImportPreview = {
  headers: ['ID', 'name', 'extra'],
  rows: [
    ['1', 'alice', 'x'],
    ['2', null, 'y'],
  ],
  columns: [
    { name: 'id', type: 'integer', nullable: false, hasDefault: false, isPK: true },
    { name: 'name', type: 'text', nullable: true, hasDefault: false, isPK: false },
  ],
  mapping: [
    { source: 0, column: 'id' },
    { source: 1, column: 'name' },
  ],
}

function fakeFiles(over: Partial<FilesApi> = {}): FilesApi {
  return {
    export: vi.fn(),
    cancelExport: vi.fn(),
    checkQuery: vi.fn(),
    saveText: vi.fn(),
    previewImport: vi.fn(async () => preview),
    runImport: vi.fn(async (): Promise<ImportResult> => ({ ok: true, rows: 2, needsConfirm: false, failure: null })),
    cancelImport: vi.fn(async () => true),
    ...over,
  }
}

function setup(over: Partial<ImportWizardProps> = {}) {
  const props: ImportWizardProps = {
    sessionId: 's1',
    schema: 'public',
    table: 'users',
    safeMode: 'off',
    onClose: vi.fn(),
    onImported: vi.fn(),
    api: fakeFiles(),
    pickFile: vi.fn(async () => '/data/users.csv'),
    ...over,
  }
  const user = userEvent.setup()
  render(<ImportWizard {...props} />)
  return { props, user }
}

describe('ImportWizard', () => {
  beforeEach(() => {
    resetBindings()
    listeners.clear()
  })
  afterEach(cleanup)

  it('previews, maps (auto, remap, ignore) and imports', async () => {
    let finish: (r: ImportResult) => void = () => {}
    const api = fakeFiles({ runImport: vi.fn(() => new Promise<ImportResult>((r) => (finish = r))) })
    const { props, user } = setup({ api })
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    expect(screen.getByRole('alert')).toHaveTextContent('Choose a CSV file.')
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(screen.getByLabelText('File')).toHaveValue('/data/users.csv')
    await user.selectOptions(screen.getByLabelText('Delimiter'), ';')
    await user.selectOptions(screen.getByLabelText('Quote'), "'")
    await user.type(screen.getByLabelText('NULL marker'), '\\N')
    await user.clear(screen.getByLabelText('Rows to skip'))
    await user.type(screen.getByLabelText('Rows to skip'), '2')
    await user.click(screen.getByRole('checkbox', { name: 'Empty fields are NULL' }))
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    expect(api.previewImport).toHaveBeenCalledWith('s1', '/data/users.csv', { delimiter: ';', header: true, quote: "'", encoding: 'utf-8', nullMarker: '\\N', emptyAsNull: true, skip: 2 }, 'public', 'users')

    // Auto-mapped by name; the unmatched column is ignored. NULLs are shown.
    expect(await screen.findByLabelText('Target for ID')).toHaveValue('id')
    expect(screen.getByLabelText('Target for name')).toHaveValue('name')
    expect(screen.getByLabelText('Target for extra')).toHaveValue('')
    expect(within(screen.getByRole('table')).getByText('NULL')).toBeInTheDocument()

    // A column mapped twice blocks the import.
    await user.selectOptions(screen.getByLabelText('Target for extra'), 'name')
    expect(screen.getByText('Column name is mapped more than once.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Import' })).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('Target for name'), '')
    await user.click(screen.getByRole('button', { name: 'Import' }))
    expect(api.runImport).toHaveBeenCalledWith(
      expect.any(String),
      's1',
      expect.objectContaining({ path: '/data/users.csv', schema: 'public', table: 'users', mapping: [{ source: 0, column: 'id' }, { source: 2, column: 'name' }] }),
      false,
    )
    const jobId = (api.runImport as ReturnType<typeof vi.fn>).mock.calls[0][0]
    act(() => listeners.get(IMPORT_PROGRESS_EVENT)!({ jobId, rows: 500 }))
    expect(screen.getByText(/500 rows inserted/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel import' }))
    expect(api.cancelImport).toHaveBeenCalledWith(jobId)
    await act(async () => finish({ ok: true, rows: 2, needsConfirm: false, failure: null }))
    expect(screen.getByText('Imported 2 rows.')).toBeInTheDocument()
    expect(props.onImported).toHaveBeenCalledWith(2)
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(props.onClose).toHaveBeenCalled()
  })

  it('shows a failing line and column, and confirm mode asks first', async () => {
    const runImport = vi
      .fn()
      .mockResolvedValueOnce({ ok: false, rows: 0, needsConfirm: true, failure: null })
      .mockResolvedValueOnce({ ok: false, rows: 0, needsConfirm: false, failure: { line: 7, column: 'id', message: 'invalid input syntax for type integer: "x"' } })
      .mockRejectedValueOnce(new Error('connection lost'))
    const { user } = setup({ api: fakeFiles({ runImport }), safeMode: 'confirm' })
    await user.type(screen.getByLabelText('File'), '/d.csv')
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await user.click(await screen.findByRole('button', { name: 'Import' }))
    expect(await screen.findByRole('note')).toHaveTextContent('asks before writing')
    await user.click(screen.getByRole('button', { name: 'Import anyway' }))
    expect(runImport).toHaveBeenLastCalledWith(expect.any(String), 's1', expect.anything(), true)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Line 7, column id:')
    expect(alert).toHaveTextContent('Nothing was imported.')
    await user.click(screen.getByRole('button', { name: 'Import' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('connection lost')
    await user.click(screen.getByRole('button', { name: 'Back' }))
    expect(screen.getByLabelText('File')).toHaveValue('/d.csv')
  })

  it('reports preview errors, refuses read-only and closes on Escape', async () => {
    const api = fakeFiles({ previewImport: vi.fn(async () => Promise.reject(new Error('line 3: the file is not valid UTF-8'))) })
    const { props, user } = setup({ api, pickFile: vi.fn(async () => Promise.reject(new Error('no dialogs'))) })
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(screen.getByRole('alert')).toHaveTextContent('no dialogs')
    await user.type(screen.getByLabelText('File'), '/bad.csv')
    await user.click(screen.getByRole('checkbox', { name: 'First row is a header' }))
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    expect(await screen.findByText('line 3: the file is not valid UTF-8')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(props.onClose).toHaveBeenCalled()
    cleanup()
    setup({ safeMode: 'readonly' })
    expect(screen.getByRole('alert')).toHaveTextContent('read-only safe mode')
    expect(screen.getByRole('button', { name: 'Preview' })).toBeDisabled()
  })

  it('maps by column and wraps the bindings', async () => {
    expect(mappingByColumn(preview, [{ source: 2, column: 'id' }])).toEqual(['', '', 'id'])
    ImportService.Preview.mockResolvedValue({ headers: null, rows: null, columns: null, mapping: null })
    expect(await filesApi.previewImport('s', '/p', {} as never, 'a', 'b')).toEqual({ headers: [], rows: [], columns: [], mapping: [] })
    ImportService.Preview.mockResolvedValue(null)
    await expect(filesApi.previewImport('s', '/p', {} as never, 'a', 'b')).rejects.toThrow('empty response')
    ImportService.Import.mockResolvedValue({ ok: true, rows: 1 })
    expect(await filesApi.runImport('j', 's', {} as never, false)).toEqual({ ok: true, rows: 1 })
    ImportService.Import.mockResolvedValue(null)
    await expect(filesApi.runImport('j', 's', {} as never, false)).rejects.toThrow('empty response')
    await filesApi.cancelImport('j')
    expect(ImportService.Cancel).toHaveBeenCalledWith('j')
  })
})
