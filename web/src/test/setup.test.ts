import { describe, expect, it } from 'vitest'

describe('jsdom 几何 API polyfill', () => {
  it('为 Semi Typography 提供 Range.getBoundingClientRect', () => {
    const range = document.createRange()

    expect(typeof range.getBoundingClientRect).toBe('function')

    const rect = range.getBoundingClientRect()
    expect(rect.width).toBe(0)
    expect(rect.height).toBe(0)
  })
})
