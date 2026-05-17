/// <reference types="vitest" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const ReactCompilerConfig = {};

export default defineConfig({
    plugins: [
        react({
            babel: {
                plugins: [['babel-plugin-react-compiler', ReactCompilerConfig]],
            },
        }),
    ],
    build: {
        outDir: 'dist',
        emptyOutDir: true,
    },
    test: {
        environment: 'jsdom',
        setupFiles: ['./src/test-setup.ts'],
        css: true,
        server: {
            deps: {
                // Gravity UI ships CSS files alongside ESM; instruct the Vite
                // dev server used by Vitest to treat them as inline so they
                // are not rejected as unknown extensions.
                inline: ['@gravity-ui/uikit', '@gravity-ui/navigation'],
            },
        },
    },
    server: {
        host: '::',
        port: 3000,
        allowedHosts: ['yanet-dev-esafronov.vla.yp-c.yandex.net'],
        proxy: {
            '/api': {
                target: 'http://localhost:8081',
                changeOrigin: true,
            },
        },
    },
});
