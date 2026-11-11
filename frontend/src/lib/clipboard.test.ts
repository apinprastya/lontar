import { afterEach, describe, expect, it, vi } from 'vitest'

const setText = vi.fn<(t: string) => Promise<void>>()
vi.mock('@wailsio/runtime', () => ({ Clipboard: { SetText: (t: string) => setText(t) } }))

import { copyText } from './clipboard'

describe('copyText', () => {
  afterEach(() => setText.mockReset())

  it('uses the Wails clipboard', async () => {
    setText.mockResolvedValue()
    await copyText('a\tb')
    expect(setText).toHaveBeenCalledWith('a\tb')
  })

  it('falls back to navigator.clipboard', async () => {
    setText.mockRejectedValue(new Error('no runtime'))
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await copyText('x')
    expect(writeText).toHaveBeenCalledWith('x')
  })
})
