import { defineConfig } from 'vite';

export default defineConfig({
    server: {
        port: 3001,
        proxy: {
            '/api': {
                target: 'http://localhost:8090',
                changeOrigin: true,
                ws: true,
            },
        },
    },
    build: {
        outDir: 'dist',
    },
});
