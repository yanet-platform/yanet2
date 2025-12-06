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
