/// <reference types="vitest" />
import { fileURLToPath } from 'node:url';
import { defineConfig, searchForWorkspaceRoot } from 'vite';
import react from '@vitejs/plugin-react';

const ReactCompilerConfig = {};

const rootDir = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
    plugins: [
        react({
            babel: {
                plugins: [['babel-plugin-react-compiler', ReactCompilerConfig]],
            },
        }),
    ],
    resolve: {
        // Shared core lives in src/core and is imported as @yanet/core/*.
        alias: {
            '@yanet/core': fileURLToPath(new URL('./src/core', import.meta.url)),
        },
        // Co-located module web sources (phase 4+) live outside web/ and have
        // no node_modules ancestor; force a single React copy to avoid a
        // duplicate instance and invalid-hook-call crashes.
        dedupe: ['react', 'react-dom'],
    },
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
        // Allow the dev server to read co-located module web sources above
        // web/ (phase 4+). Keep searchForWorkspaceRoot or auto workspace
        // detection silently turns off.
        fs: {
            allow: [
                searchForWorkspaceRoot(rootDir),
                fileURLToPath(new URL('..', import.meta.url)),
            ],
        },
        proxy: {
            '/api': {
                target: 'http://localhost:8081',
                changeOrigin: true,
            },
        },
    },
});
