import { Marked } from 'marked';
import hljs from 'highlight.js';

// Create marked instance with syntax highlighting
const marked = new Marked({
	gfm: true,
	breaks: true,
	renderer: {
		code(code, language) {
			const lang = language && hljs.getLanguage(language) ? language : 'plaintext';
			const highlighted = hljs.highlight(code, { language: lang }).value;
			return `<pre class="hljs rounded-md overflow-x-auto"><code class="language-${lang}">${highlighted}</code></pre>`;
		}
	}
});

// Parse @mentions and convert to links
function parseMentions(text, orgSlug) {
	// Match @username patterns (alphanumeric, underscores, hyphens)
	return text.replace(/@([a-zA-Z0-9_-]+)/g, (match, username) => {
		// For now, just make them visually distinct
		// Could link to user profile if we add that feature
		return `<span class="mention text-blue-400 font-medium">@${username}</span>`;
	});
}

// Render markdown with @mentions and syntax highlighting
export function renderMarkdown(text, orgSlug = '') {
	if (!text) return '';

	// First parse mentions (before markdown to preserve them)
	const withMentions = parseMentions(text, orgSlug);

	// Then render markdown
	const html = marked.parse(withMentions);

	return html;
}

// Render inline markdown (no block elements, for single-line contexts)
export function renderInlineMarkdown(text, orgSlug = '') {
	if (!text) return '';

	const withMentions = parseMentions(text, orgSlug);
	const html = marked.parseInline(withMentions);

	return html;
}

// Extract mentions from text (for notification purposes)
export function extractMentions(text) {
	if (!text) return [];
	const matches = text.match(/@([a-zA-Z0-9_-]+)/g);
	if (!matches) return [];
	return [...new Set(matches.map(m => m.slice(1)))]; // Remove @ and dedupe
}
