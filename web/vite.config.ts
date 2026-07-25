import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { viteSingleFile } from 'vite-plugin-singlefile'

const root = path.dirname(fileURLToPath(import.meta.url))

// VITE_HOSTED=1: base 指向 CPA 资源挂载路径（与 key-policy 同一模式）。
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  base: process.env.VITE_HOSTED === '1' ? '/v0/resource/plugins/model-mapper-plus/' : '/',
  resolve: {
    // semi.min.css is not listed in package "exports"; alias so Vite 8 can resolve it.
    alias: {
      '@semi-css': path.resolve(root, 'node_modules/@douyinfe/semi-ui/dist/css/semi.min.css'),
    },
  },
  build: {
    assetsInlineLimit: 100000000,
    cssCodeSplit: false,
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
