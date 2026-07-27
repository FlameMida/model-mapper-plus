import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// 与 vite.config.ts 分离，避免污染构建配置；测试不依赖 CSS alias（jsdom 下无意义）。
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    server: {
      deps: {
        // Semi 的 CJS 构建里有 require("...icons.css")，不 inline 时 vitest 把 css 当 JS
        // 解析（报 Unexpected token）。整条链交给 Vite 转换才能正确处理 css。
        inline: ['@douyinfe/semi-ui', '@douyinfe/semi-icons', '@douyinfe/semi-foundation'],
      },
    },
  },
})
