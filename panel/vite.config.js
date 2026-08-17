import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  base: './',
  build: { outDir: '../internal/panel/dist', emptyOutDir: true },
  server: { port: 5171, proxy: { '/api': 'http://100.90.163.54:7700' } },
  test: { include: ['src/**/*.test.ts'], environment: 'node' }
});
