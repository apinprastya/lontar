import { render } from '@testing-library/react'
import { Group, Panel, Separator } from 'react-resizable-panels'
import { describe, expect, it } from 'vitest'
import { layoutStorage, usePanelLayout } from './panelLayout'
import { getUIState, setUIState } from './uiState'

describe('layoutStorage', () => {
  it('passes valid layouts through and hides invalid ones', () => {
    expect(layoutStorage.getItem('k')).toBeNull()
    layoutStorage.setItem('k', '{"a":30,"b":70}')
    expect(getUIState('k')).toBe('{"a":30,"b":70}')
    expect(layoutStorage.getItem('k')).toBe('{"a":30,"b":70}')
    for (const bad of ['{oops', '[1,2]', 'null', '{"a":"30"}']) {
      setUIState('k', bad)
      expect(layoutStorage.getItem('k')).toBeNull()
    }
  })
})

function Split({ ids }: { ids?: string[] }) {
  const layout = usePanelLayout('test', ids)
  return (
    <Group orientation="horizontal" {...layout}>
      <Panel id="a">A</Panel>
      <Separator />
      <Panel id="b">B</Panel>
    </Group>
  )
}

describe('usePanelLayout', () => {
  it('renders a Group from a saved layout and survives a corrupt one', () => {
    setUIState('react-resizable-panels:layout.test', '{"a":25,"b":75}')
    const { unmount, getByText } = render(<Split />)
    expect(getByText('A')).toBeInTheDocument()
    unmount()
    setUIState('react-resizable-panels:layout.test', '{corrupt')
    expect(() => render(<Split ids={['a', 'b']} />)).not.toThrow()
  })
})
