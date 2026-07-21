/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5190,
    host: '0.0.0.0',
    proxy: {
      '/api': { target: 'http://localhost:9015', changeOrigin: true },
    },
  },
  preview: {
    port: 5191,
    host: '0.0.0.0',
    allowedHosts: ['soulman.breynisson.org'],
    proxy: {
      '/api': { target: 'http://localhost:9005', changeOrigin: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/setupTests.ts'],
  },
})
