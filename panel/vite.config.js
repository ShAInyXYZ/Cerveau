import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  base: './',
  build: { outDir: '../internal/panel/dist', emptyOutDir: true },
  server: { port: 5171, proxy: { '/api': 'http://localhost:7700' } }
});
