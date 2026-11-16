/// <reference types="vitest/config" />
import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';

// Build trafia do web/dist i jest osadzany w binarce (//go:embed all:web/dist).
export default defineConfig({
  plugins: [react()],
  build: {outDir: 'dist', emptyOutDir: true, sourcemap: false},
  server: {proxy: {'/api': 'http://127.0.0.1:8080'}},
  test: {environment: 'jsdom', globals: true, setupFiles: ['./src/test/setup.ts'], css: false},
});
