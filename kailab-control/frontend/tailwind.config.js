/** @type {import('tailwindcss').Config} */
export default {
	darkMode: ['selector', '[data-theme="dark"]'],
	content: ['./src/**/*.{html,js,svelte,ts}'],
	theme: {
		extend: {
			colors: {
				'kai-bg': 'rgb(var(--kai-bg) / <alpha-value>)',
				'kai-bg-secondary': 'rgb(var(--kai-bg-secondary) / <alpha-value>)',
				'kai-bg-tertiary': 'rgb(var(--kai-bg-tertiary) / <alpha-value>)',
				'kai-border': 'rgb(var(--kai-border) / <alpha-value>)',
				'kai-border-muted': 'rgb(var(--kai-border-muted) / <alpha-value>)',
				'kai-text': 'rgb(var(--kai-text) / <alpha-value>)',
				'kai-text-muted': 'rgb(var(--kai-text-muted) / <alpha-value>)',
				'kai-accent': 'rgb(var(--kai-accent) / <alpha-value>)',
				'kai-accent-hover': 'rgb(var(--kai-accent-hover) / <alpha-value>)',
				'kai-success': 'rgb(var(--kai-success) / <alpha-value>)',
				'kai-success-emphasis': 'rgb(var(--kai-success-emphasis) / <alpha-value>)',
				'kai-error': 'rgb(var(--kai-error) / <alpha-value>)',
				'kai-warning': 'rgb(var(--kai-warning) / <alpha-value>)',
				'kai-purple': 'rgb(var(--kai-purple) / <alpha-value>)'
			}
		}
	},
	plugins: []
};
