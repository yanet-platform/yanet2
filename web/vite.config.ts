/// <reference types="vitest" />
import { fileURLToPath } from 'node:url';
import { defineConfig, searchForWorkspaceRoot, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

const ReactCompilerConfig = {};

const rootDir = fileURLToPath(new URL('.', import.meta.url));

// Serve the SPA shell for HTML navigations whose path collides with a
// co-located source directory.
//
// Built-in pages live under web/builtin/<page>/ with an index.ts barrel while
// their routes are /builtin/<page>. On a hard reload the dev server otherwise
// resolves the bare path to that directory's index module and serves it as
// JavaScript instead of index.html. Rewriting extensionless HTML GET requests
// to / forces the SPA fallback before that directory-index resolution runs.
const spaHtmlFallback = (): Plugin => ({
    name: 'yanet-spa-html-fallback',
    configureServer(server) {
        server.middlewares.use((req, _res, next) => {
            const accept = String(req.headers.accept ?? '');
            const url = (req.url ?? '').split('?')[0];
            if (
                req.method === 'GET' &&
                accept.includes('text/html') &&
                url !== '/' &&
                !url.startsWith('/@') &&
                !url.startsWith('/api') &&
                !/\.[^/]+$/.test(url)
            ) {
                req.url = '/';
            }
            next();
        });
    },
});

export default defineConfig({
    plugins: [
        spaHtmlFallback(),
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
        // Co-located feature specs live outside web/ (phase 4+). Vitest's
        // default include is web-relative, so the sibling roots are listed
        // explicitly or their *.test.* files silently stop running in CI.
        include: [
            '**/*.{test,spec}.?(c|m)[jt]s?(x)',
            '../modules/*/web/**/*.{test,spec}.?(c|m)[jt]s?(x)',
            '../operators/*/web/**/*.{test,spec}.?(c|m)[jt]s?(x)',
            '../devices/*/web/**/*.{test,spec}.?(c|m)[jt]s?(x)',
        ],
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
