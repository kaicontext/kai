import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit()],
	server: {
		proxy: {
			// Control plane API
			'/api': 'http://localhost:8080',
			'/health': 'http://localhost:8080',
			// Data plane proxy - match /{org}/{repo}/v1/
			// Use a regex-like pattern to catch org/repo paths
			'^/[^/]+/[^/]+/v1': {
				target: 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
