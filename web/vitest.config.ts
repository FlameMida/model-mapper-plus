import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// 与 vite.config.ts 分离，避免污染构建配置；测试不依赖 CSS alias（jsdom 下无意义）。
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
})
