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

import { SQLRunService, resetBindings } from '../../test/mockBindings'
import RunSqlFileDialog, { dumpLabel, percent, resultStatus, runOptions, type RunSqlFileDialogProps } from './RunSqlFileDialog'
import { RUN_PROGRESS_EVENT, sqlRunApi, type RunFileResult, type RunResult, type SqlFileInfo, type SqlRunApi } from './api'

const info: SqlFileInfo = {
  path: '/dumps/shop.sql',
  name: 'shop.sql',
  size: 3 * 1024 * 1024,
  dumpType: 'pg_dump',
  hasCopy: true,
  statements: 1234,
  exact: false,
  warning: '',
}

const okResult: RunResult = {
  run: 10,
  failed: 0,
  skipped: 2,
  rowsCopied: 5000,
  errors: [],
  cancelled: false,
  stopped: false,
  rolledBack: false,
  durationMs: 1500,
}

function fakeApi(over: Partial<SqlRunApi> = {}): SqlRunApi {
  return {
    pickAndInspect: vi.fn(async () => info),
    inspect: vi.fn(async () => info),
    runFile: vi.fn(async (): Promise<RunFileResult> => ({ needsConfirm: false, writes: 0, writeStatements: [], result: okResult })),
    cancel: vi.fn(async () => true),
    ...over,
  }
}

function setup(over: Partial<RunSqlFileDialogProps> = {}) {
  const props: RunSqlFileDialogProps = {
    sessionId: 's1',
    safeMode: 'off',
    onClose: vi.fn(),
    onRan: vi.fn(),
    onOpenStatement: vi.fn(),
    api: fakeApi(),
    ...over,
  }
  const user = userEvent.setup()
  render(<RunSqlFileDialog {...props} />)
  return { props, user }
}

describe('RunSqlFileDialog', () => {
  beforeEach(() => {
    resetBindings()
    listeners.clear()
  })
  afterEach(cleanup)

  it('inspects, runs in one transaction with progress and cancel, and shows the result', async () => {
    let finish: (r: RunFileResult) => void = () => {}
    const api = fakeApi({ runFile: vi.fn(() => new Promise<RunFileResult>((r) => (finish = r))) })
    const { props, user } = setup({ api })
    expect(screen.getByRole('dialog', { name: 'Run SQL file' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(api.pickAndInspect).toHaveBeenCalledWith('s1')
    expect(screen.getByText('shop.sql')).toBeInTheDocument()
    expect(screen.getByText('3.0 MiB')).toBeInTheDocument()
    expect(screen.getByText('pg_dump (PostgreSQL plain format) · COPY data')).toBeInTheDocument()
    expect(screen.getByText('about 1,234')).toBeInTheDocument()

    const group = screen.getByRole('radiogroup', { name: 'On errors' })
    expect(within(group).getByRole('radio', { name: /Stop at the first error/ })).toBeChecked()
    await user.click(within(group).getByRole('radio', { name: /Run in one transaction/ }))
    expect(screen.getByText(/own BEGIN\/COMMIT are skipped/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Run' }))
    expect(api.runFile).toHaveBeenCalledWith(expect.any(String), 's1', '/dumps/shop.sql', { singleTransaction: true, continueOnError: false }, false)
    const jobId = (api.runFile as ReturnType<typeof vi.fn>).mock.calls[0][0]
    expect(screen.getByText('Starting…')).toBeInTheDocument()

    // Progress of another job is ignored; this job's is shown.
    act(() => listeners.get(RUN_PROGRESS_EVENT)!({ jobId: 'other', phase: 'run', bytesRead: 1, totalBytes: 2, statements: 1, rowsCopied: 0 }))
    expect(screen.getByText('Starting…')).toBeInTheDocument()
    act(() => listeners.get(RUN_PROGRESS_EVENT)!({ jobId, phase: 'run', bytesRead: 50, totalBytes: 200, statements: 7, rowsCopied: 1200 }))
    expect(screen.getByText('25% · 7 statements · 1,200 rows copied')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'Progress' })).toHaveAttribute('value', '25')
    // Escape does not close a running dialog.
    await user.keyboard('{Escape}')
    expect(props.onClose).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Cancel run' }))
    expect(api.cancel).toHaveBeenCalledWith(jobId)
    expect(screen.getByText('Cancelling…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel run' })).toBeDisabled()

    await act(async () => finish({ needsConfirm: false, writes: 0, writeStatements: [], result: { ...okResult, cancelled: true, rolledBack: true, run: 3 } }))
    expect(screen.getByRole('status')).toHaveTextContent('Cancelled: everything was rolled back. 3 statements ran, 2 skipped, 5,000 rows copied in 1.5 s.')
    expect(props.onRan).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(props.onClose).toHaveBeenCalled()
  })

  it('asks once in confirm mode and lists the errors, which open in a query tab', async () => {
    const failed: RunResult = {
      ...okResult,
      run: 4,
      failed: 3,
      skipped: 0,
      rowsCopied: 0,
      stopped: false,
      errors: [
        { line: 12, preview: 'INSERT INTO t VALUES (1)', message: 'duplicate key value', sql: 'INSERT INTO t VALUES (1)' },
        { line: 40, preview: 'CREATE TABLE big …', message: 'syntax error', sql: '' },
      ],
    }
    const runFile = vi
      .fn()
      .mockResolvedValueOnce({
        needsConfirm: true,
        writes: 25,
        writeStatements: [
          { line: 3, preview: 'CREATE TABLE t (a int)' },
          { line: 4, preview: 'INSERT INTO t VALUES (1)' },
        ],
        result: null,
      })
      .mockResolvedValueOnce({ needsConfirm: false, writes: 0, writeStatements: [], result: failed })
    const { props, user } = setup({ api: fakeApi({ runFile }), safeMode: 'confirm' })
    await user.type(screen.getByLabelText('File'), '/d.sql{Enter}')
    await user.click(await screen.findByRole('radio', { name: /Continue after errors/ }))
    await user.click(screen.getByRole('button', { name: 'Run' }))
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('asks before writing')
    expect(note).toHaveTextContent('25 statements that change data or schema; the first 2:')
    expect(within(note).getByText('CREATE TABLE t (a int)')).toBeInTheDocument()
    expect(props.onRan).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Run anyway' }))
    expect(runFile).toHaveBeenLastCalledWith(expect.any(String), 's1', '/dumps/shop.sql', { singleTransaction: false, continueOnError: true }, true)

    expect(await screen.findByRole('status')).toHaveTextContent('Finished with errors. 4 statements ran, 3 failed in 1.5 s.')
    expect(screen.getByText(/The first 2 errors/)).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Errors' })
    const buttons = within(list).getAllByRole('button')
    expect(buttons[0]).toHaveTextContent('Line 12INSERT INTO t VALUES (1)duplicate key value')
    // A statement without its full text opens its preview.
    await user.click(buttons[1])
    expect(props.onOpenStatement).toHaveBeenCalledWith('CREATE TABLE big …')
    expect(props.onClose).toHaveBeenCalled()
  })

  it('reports errors, refusals and empty answers, and goes back', async () => {
    const runFile = vi
      .fn()
      .mockRejectedValueOnce(new Error('This connection is in read-only safe mode and the file writes (line 2: INSERT …). Nothing was run.'))
      .mockResolvedValueOnce({ needsConfirm: false, writes: 0, writeStatements: [], result: null })
    const api = fakeApi({
      pickAndInspect: vi.fn().mockResolvedValueOnce(null).mockRejectedValueOnce(new Error('no dialogs')),
      inspect: vi.fn().mockRejectedValueOnce(new Error('open /x.sql: no such file')).mockResolvedValue({ ...info, warning: 'This looks like a mysqldump file', exact: true }),
      runFile,
    })
    const { props, user } = setup({ api, safeMode: 'readonly' })
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByRole('alert')).toHaveTextContent('Choose a SQL file.')
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(screen.getByLabelText('File')).toBeInTheDocument() // cancelled: still choosing
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(screen.getByRole('alert')).toHaveTextContent('no dialogs')
    await user.type(screen.getByLabelText('File'), '/x.sql')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no such file')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByRole('note')).toHaveTextContent('looks like a mysqldump file')
    expect(screen.getByText('1,234')).toBeInTheDocument()
    expect(screen.getByText(/Read-only safe mode/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Run' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('read-only safe mode and the file writes')
    await user.click(screen.getByRole('button', { name: 'Run' }))
    expect(await screen.findByRole('button', { name: 'Run' })).toBeInTheDocument()
    expect(props.onRan).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Back' }))
    expect(screen.getByLabelText('File')).toHaveValue('/dumps/shop.sql')
    await user.click(screen.getByLabelText('File'))
    await user.keyboard('{Escape}')
    expect(props.onClose).toHaveBeenCalled()
  })

  it('shows the scan phase and a clean finish', async () => {
    let finish: (r: RunFileResult) => void = () => {}
    const api = fakeApi({ runFile: vi.fn(() => new Promise<RunFileResult>((r) => (finish = r))) })
    const { props, user } = setup({ api })
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    await user.click(screen.getByRole('button', { name: 'Run' }))
    const jobId = (api.runFile as ReturnType<typeof vi.fn>).mock.calls[0][0]
    act(() => listeners.get(RUN_PROGRESS_EVENT)!({ jobId, phase: 'scan', bytesRead: 10, totalBytes: 20, statements: 0, rowsCopied: 0 }))
    expect(screen.getByText('Checking the file for writes… 50%')).toBeInTheDocument()
    act(() => listeners.get(RUN_PROGRESS_EVENT)!({ jobId, phase: 'run', bytesRead: 20, totalBytes: 20, statements: 1, rowsCopied: 0 }))
    expect(screen.getByText('100% · 1 statement')).toBeInTheDocument()
    await act(async () => finish({ needsConfirm: false, writes: 0, writeStatements: [], result: { ...okResult, run: 1, skipped: 0, rowsCopied: 1 } }))
    expect(screen.getByRole('status')).toHaveTextContent('Finished. 1 statement ran, 1 row copied in 1.5 s.')
    expect(screen.getByRole('status')).toHaveClass('ok')
    expect(screen.queryByRole('list', { name: 'Errors' })).not.toBeInTheDocument()
    expect(props.onRan).toHaveBeenCalled()
  })

  it('has helpers and wraps the bindings', async () => {
    expect(runOptions('stop')).toEqual({ singleTransaction: false, continueOnError: false })
    expect(runOptions('continue')).toEqual({ singleTransaction: false, continueOnError: true })
    expect(dumpLabel('sqlite')).toBe('sqlite3 .dump')
    expect(dumpLabel('other')).toBe('other')
    expect(percent({ bytesRead: 5, totalBytes: 0 })).toBe(0)
    expect(percent({ bytesRead: 30, totalBytes: 20 })).toBe(100)
    const base = { ...okResult, cancelled: false, rolledBack: false, stopped: false, failed: 0 }
    expect(resultStatus({ ...base, cancelled: true })).toBe('Cancelled: the statements that ran before stay.')
    expect(resultStatus({ ...base, rolledBack: true, failed: 1 })).toBe('Stopped at an error: everything was rolled back.')
    expect(resultStatus({ ...base, stopped: true, failed: 1 })).toBe('Stopped at an error: the statements before it stay.')

    SQLRunService.PickAndInspect.mockResolvedValue(info)
    expect(await sqlRunApi.pickAndInspect('s')).toEqual(info)
    SQLRunService.PickAndInspect.mockResolvedValue(null)
    expect(await sqlRunApi.pickAndInspect('s')).toBeNull()
    SQLRunService.Inspect.mockResolvedValue(info)
    expect(await sqlRunApi.inspect('s', '/p')).toEqual(info)
    SQLRunService.Inspect.mockResolvedValue(null)
    await expect(sqlRunApi.inspect('s', '/p')).rejects.toThrow('empty response')
    SQLRunService.RunFile.mockResolvedValue({ needsConfirm: true, writes: 2, writeStatements: null, result: null })
    expect(await sqlRunApi.runFile('j', 's', '/p', runOptions('stop'), false)).toEqual({ needsConfirm: true, writes: 2, writeStatements: [], result: null })
    SQLRunService.RunFile.mockResolvedValue({ needsConfirm: false, writes: 0, writeStatements: [], result: { ...okResult, errors: null } })
    expect((await sqlRunApi.runFile('j', 's', '/p', runOptions('stop'), true)).result?.errors).toEqual([])
    SQLRunService.RunFile.mockResolvedValue(null)
    await expect(sqlRunApi.runFile('j', 's', '/p', runOptions('stop'), true)).rejects.toThrow('empty response')
    expect(await sqlRunApi.cancel('j')).toBe(true)
    expect(SQLRunService.Cancel).toHaveBeenCalledWith('j')
  })
})
