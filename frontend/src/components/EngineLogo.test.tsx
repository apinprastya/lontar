import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { Kind } from '../features/connections/types'
import { EngineBadge, EngineLogo, engineColor } from './EngineLogo'

describe('engine logos', () => {
  afterEach(cleanup)

  it('has a logo and brand colour for every engine', () => {
    for (const kind of ['postgres', 'mysql', 'mariadb', 'sqlite'] as Kind[]) {
      const { container, unmount } = render(<EngineBadge kind={kind} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toMatch(/^M/)
      expect(engineColor(kind)).toMatch(/^#[0-9A-F]{6}$/i)
      unmount()
    }
    expect(engineColor('postgres')).toBe('#4169E1')
  })

  it('names the logo when titled and falls back to text for unknown engines', () => {
    render(<EngineLogo kind="sqlite" title="SQLite" />)
    expect(screen.getByRole('img', { name: 'SQLite' })).toBeInTheDocument()
    const unknown = 'oracle' as Kind
    const { container } = render(<EngineBadge kind={unknown} className="x" />)
    expect(container.querySelector('svg')).toBeNull()
    expect(container.querySelector('.kind-badge.x')).toHaveTextContent('?oracle')
    expect(engineColor(unknown)).toBe('#6b6b76')
    render(<EngineLogo kind={unknown} />)
  })
})
