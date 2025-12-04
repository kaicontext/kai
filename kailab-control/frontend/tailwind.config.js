/** @type {import('tailwindcss').Config} */
export default {
	content: ['./src/**/*.{html,js,svelte,ts}'],
	theme: {
		extend: {
			colors: {
				// GitHub dark theme colors
				'kai-bg': '#0d1117',
				'kai-bg-secondary': '#161b22',
				'kai-bg-tertiary': '#21262d',
				'kai-border': '#30363d',
				'kai-border-muted': '#21262d',
				'kai-text': '#e6edf3',
				'kai-text-muted': '#8b949e',
				'kai-accent': '#58a6ff',
				'kai-accent-hover': '#79c0ff',
				// GitHub's muted green (not neon)
				'kai-success': '#238636',
				'kai-success-emphasis': '#2ea043',
				'kai-error': '#f85149',
				'kai-warning': '#d29922',
				'kai-purple': '#a371f7'
			}
		}
	},
	plugins: []
};
