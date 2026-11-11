import type { StatementResult } from './types'

const fmt = (n: number) => n.toLocaleString('en-US')

/** "0.42 ms", "12.3 ms", "456 ms", "1.23 s". */
export function formatDuration(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`
  if (ms >= 100) return `${Math.round(ms)} ms`
  if (ms >= 10) return `${ms.toFixed(1)} ms`
  return `${ms.toFixed(2)} ms`
}

/** The first non-blank line of a statement, shortened. */
export function firstLine(sql: string, max = 120): string {
  const line = sql.split('\n').find((l) => l.trim() !== '') ?? ''
  const t = line.trim()
  const more = t.length > max || sql.trim().includes('\n')
  return t.length > max ? `${t.slice(0, max)}…` : more ? `${t} …` : t
}

/** Rows returned/affected by a statement, for the Messages tab. */
export function statementSummary(st: StatementResult): string {
  switch (st.status) {
    case 'skipped':
      return 'Not run'
    case 'cancelled':
      return 'Cancelled'
    case 'error':
      return 'Failed'
  }
  if (st.resultSets.length > 0) {
    const rows = st.resultSets.reduce((n, rs) => n + rs.rows.length, 0)
    const more = st.resultSets.some((rs) => rs.truncated)
    const sets = st.resultSets.length > 1 ? ` in ${st.resultSets.length} result sets` : ''
    return `${fmt(rows)}${more ? '+' : ''} row${rows === 1 && !more ? '' : 's'} returned${sets}`
  }
  if (st.rowsAffected !== null) return `${fmt(st.rowsAffected)} row${st.rowsAffected === 1 ? '' : 's'} affected`
  return 'OK'
}

/** "1,000 rows" / "1,000+ rows" for the status bar. */
export function rowCountText(n: number, more: boolean): string {
  return `${fmt(n)}${more ? '+' : ''} row${n === 1 && !more ? '' : 's'}`
}

/** Relative-ish time for the history list: "14:03:12" today, else the date. */
export function historyTime(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const sameDay = d.toDateString() === now.toDateString()
  const pad = (n: number) => String(n).padStart(2, '0')
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  return sameDay ? time : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${time}`
}
