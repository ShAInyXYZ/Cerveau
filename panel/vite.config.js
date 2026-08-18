import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  base: './',
  build: { outDir: '../internal/panel/dist', emptyOutDir: true },
  // Dev proxy target. Defaults to loopback — never bake a personal tailnet
  // address into a tracked file. Point it elsewhere with:
  //   CRV_DEV_API=http://<host>:7700 npm run dev
  server: {
    port: 5171,
    proxy: { '/api': process.env.CRV_DEV_API || 'http://127.0.0.1:7700' }
  },
  test: { include: ['src/**/*.test.ts'], environment: 'node' }
});
