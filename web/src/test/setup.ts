import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// vitest 未启用 globals，RTL 不会自动注册 cleanup；手动在每个用例后清理 DOM，避免跨用例残留。
afterEach(() => {
  cleanup()
})

// jsdom 不实现 canvas；Semi UI 的 lottie 动画（Loading/Spin）调用 canvas 2d context，
// 默认 getContext 返回 null 导致 lottie 报错。用 Proxy stub 一个吸收任意属性/方法的 context。
HTMLCanvasElement.prototype.getContext = function () {
  const ctx: Record<string, unknown> = {}
  return new Proxy(ctx, {
    get: (_t, prop) => {
      if (prop in ctx) return ctx[prop as string]
      return typeof prop === 'string' ? () => {} : undefined
    },
    set: (_t, prop, value) => {
      ctx[prop as string] = value
      return true
    },
  })
} as unknown as typeof HTMLCanvasElement.prototype.getContext
