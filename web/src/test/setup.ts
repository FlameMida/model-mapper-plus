// 与生产入口 main.tsx 一致：注入 Semi 的 React 19 createRoot 适配，否则
// Modal.confirm / Toast 等命令式 API 在 jsdom 下同样静默失效（删除绑定等用例会假红）。
import '@douyinfe/semi-ui/react19-adapter'
import '@testing-library/jest-dom/vitest'

import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// vitest 未启用 globals，RTL 不会自动注册 cleanup；手动在每个用例后清理 DOM，避免跨用例残留。
afterEach(() => {
  cleanup()
})

// jsdom 不实现 ResizeObserver；Semi UI 的 Nav/Layout/Table 等组件依赖它做尺寸观测，
// 缺失会在 mount 时抛 ReferenceError。补一个空实现（组件测试不校验尺寸观测行为）。
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver

// jsdom 不实现 Range 几何 API；Semi Typography 判断省略文本是否需要 Tooltip 时
// 会调用 getBoundingClientRect。组件测试不验证真实布局，返回零尺寸 DOMRect 即可。
if (typeof Range.prototype.getBoundingClientRect !== 'function') {
  Range.prototype.getBoundingClientRect = () => new DOMRect(0, 0, 0, 0)
}

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

// Import after the canvas shim: Semi loads lottie at module evaluation time.
// jsdom cannot run popup exit animations; real motion is covered by browser QA.
const { Select } = await import('@douyinfe/semi-ui')
Select.defaultProps.motion = false
