import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../bindings/lontar/internal/services', () => import('../test/mockBindings'))

import { SettingsService, resetBindings } from '../test/mockBindings'
import { DEFAULT_SETTINGS } from '../features/settings/types'
import { applyAppearance, bindAppearance, resolveTheme, useSettings, watchSystemTheme } from './settings'

const st = () => useSettings.getState()

describe('settings store', () => {
  beforeEach(() => {
    resetBindings()
    useSettings.setState({ settings: DEFAULT_SETTINGS, loaded: false, error: null, open: false, systemDark: true })
  })
  afterEach(() => vi.restoreAllMocks())

  it('loads, reports read errors and saves', async () => {
    SettingsService.Get.mockResolvedValue({ settings: { ...DEFAULT_SETTINGS, theme: 'light' }, error: '' })
    await st().load()
    expect(st().loaded).toBe(true)
    expect(st().settings.theme).toBe('light')
    expect(st().error).toBeNull()

    SettingsService.Get.mockResolvedValue({ settings: DEFAULT_SETTINGS, error: 'settings file is corrupt' })
    await st().load()
    expect(st().error).toBe('settings file is corrupt')

    SettingsService.Get.mockRejectedValue(new Error('backend down'))
    await st().load()
    expect(st().error).toBe('backend down')

    SettingsService.Save.mockResolvedValue({ ...DEFAULT_SETTINGS, gridPageSize: 5000 })
    expect(await st().save({ ...DEFAULT_SETTINGS, gridPageSize: 99999 })).toBe(true)
    expect(st().settings.gridPageSize).toBe(5000)
    expect(st().error).toBeNull()

    SettingsService.Save.mockRejectedValue(new Error('newer version'))
    expect(await st().save(DEFAULT_SETTINGS)).toBe(false)
    expect(st().error).toBe('newer version')

    st().setOpen(true)
    expect(st().open).toBe(true)
  })

  it('resolves and applies the theme', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
    const root = document.createElement('div')
    applyAppearance('light', 15, root)
    expect(root.dataset.theme).toBe('light')
    expect(root.style.getPropertyValue('--editor-font-size')).toBe('15px')
  })

  it('binds the appearance to the store synchronously', () => {
    const root = document.createElement('div')
    const unbind = bindAppearance(root)
    expect(root.dataset.theme).toBe('dark')
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, theme: 'light', editorFontSize: 18 } })
    expect(root.dataset.theme).toBe('light')
    expect(root.style.getPropertyValue('--editor-font-size')).toBe('18px')
    unbind()
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, theme: 'dark' } })
    expect(root.dataset.theme).toBe('light')
  })

  it('follows the OS colour scheme live for the system theme', () => {
    let listener: ((e: { matches: boolean }) => void) | null = null
    const mq = {
      matches: false,
      addEventListener: vi.fn((_: string, fn: (e: { matches: boolean }) => void) => (listener = fn)),
      removeEventListener: vi.fn(),
    }
    vi.spyOn(window, 'matchMedia').mockReturnValue(mq as unknown as MediaQueryList)
    const root = document.createElement('div')
    const unbind = bindAppearance(root)
    const unwatch = watchSystemTheme()
    expect(st().systemDark).toBe(false)
    expect(root.dataset.theme).toBe('light')
    listener!({ matches: true })
    expect(st().systemDark).toBe(true)
    expect(root.dataset.theme).toBe('dark')
    unwatch()
    unbind()
    expect(mq.removeEventListener).toHaveBeenCalled()
  })
})
