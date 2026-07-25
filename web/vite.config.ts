import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { viteSingleFile } from 'vite-plugin-singlefile'

// VITE_HOSTED=1: base 指向 CPA 资源挂载路径（与 key-policy 同一模式）。
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  base: process.env.VITE_HOSTED === '1' ? '/v0/resource/plugins/model-mapper-plus/' : '/',
  build: {
    assetsInlineLimit: 100000000,
    cssCodeSplit: false,
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
