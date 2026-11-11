import { describe, expect, it } from 'vitest'
import { errorMessage } from './errors'

describe('errorMessage', () => {
  it('handles Errors, message objects and other values', () => {
    expect(errorMessage(new Error('boom'))).toBe('boom')
    expect(errorMessage({ message: 'from wails', kind: 'RuntimeError' })).toBe('from wails')
    expect(errorMessage({ message: 1 })).toBe('[object Object]')
    expect(errorMessage('plain')).toBe('plain')
    expect(errorMessage(null)).toBe('null')
  })
})
