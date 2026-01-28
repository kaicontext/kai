<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';
	import { renderMarkdown, extractMentions } from '$lib/markdown.js';
	import hljs from 'highlight.js';

	// Get language from filename extension
	function getLanguageFromFile(filename) {
		if (!filename) return 'plaintext';
		const ext = filename.split('.').pop()?.toLowerCase();
		const langMap = {
			'js': 'javascript',
			'jsx': 'javascript',
			'ts': 'typescript',
			'tsx': 'typescript',
			'py': 'python',
			'rb': 'ruby',
			'go': 'go',
			'rs': 'rust',
			'java': 'java',
			'c': 'c',
			'cpp': 'cpp',
			'h': 'c',
			'hpp': 'cpp',
			'cs': 'csharp',
			'php': 'php',
			'swift': 'swift',
			'kt': 'kotlin',
			'scala': 'scala',
			'sh': 'bash',
			'bash': 'bash',
			'zsh': 'bash',
			'yml': 'yaml',
			'yaml': 'yaml',
			'json': 'json',
			'xml': 'xml',
			'html': 'html',
			'htm': 'html',
			'css': 'css',
			'scss': 'scss',
			'less': 'less',
			'md': 'markdown',
			'sql': 'sql',
			'graphql': 'graphql',
			'dockerfile': 'dockerfile',
			'svelte': 'html',
			'vue': 'html',
		};
		return langMap[ext] || 'plaintext';
	}

	// Highlight a single line of code
	function highlightLine(content, filename) {
		if (!content) return '';
		const lang = getLanguageFromFile(filename);
		try {
			if (hljs.getLanguage(lang)) {
				return hljs.highlight(content, { language: lang }).value;
			}
		} catch (e) {
			// Fall back to plain text
		}
		// Escape HTML for plain text
		return content.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
	}

	let review = $state(null);
	let changeset = $state(null);
	let loading = $state(true);
	let error = $state('');
	let expandedGroups = $state({});
	let aiSuggestions = $state([]);
	let aiLoading = $state(false);
	let selectedFile = $state(null);
	let fileDiff = $state(null);
	let semanticDiff = $state(null);
	let diffLoading = $state(false);
	let diffMode = $state('line'); // 'line' or 'semantic'
	let comments = $state([]);
	let newComment = $state('');
	let commentLoading = $state(false);
	let inlineCommentLine = $state(null); // {file, line, type}
	let inlineCommentText = $state('');
	let replyingTo = $state(null); // comment id being replied to
	let replyText = $state('');
	let editingIntent = $state(false);
	let intentDraft = $state('');

	// Organize comments into threads (top-level comments with their replies)
	let commentThreads = $derived(() => {
		const threads = [];
		const replyMap = new Map();

		// First pass: separate top-level comments and replies
		for (const c of comments) {
			if (c.parentId) {
				if (!replyMap.has(c.parentId)) {
					replyMap.set(c.parentId, []);
				}
				replyMap.get(c.parentId).push(c);
			} else {
				threads.push({ ...c, replies: [] });
			}
		}

		// Second pass: attach replies to their parents
		for (const thread of threads) {
			thread.replies = replyMap.get(thread.id) || [];
			// Sort replies by createdAt
			thread.replies.sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0));
		}

		// Sort threads by createdAt
		threads.sort((a, b) => (a.createdAt || 0) - (b.createdAt || 0));

		return threads;
	});

	// Group changes by category
	let changeGroups = $derived(groupChanges(changeset));

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}
		await loadReview();
	});

	async function loadReview() {
		loading = true;
		error = '';
		const { slug, repo, id } = $page.params;

		try {
			// Load reviews list and find the one we want
			const data = await api('GET', `/${slug}/${repo}/v1/reviews`);
			if (data.error) {
				error = data.error;
			} else {
				review = (data.reviews || []).find(r => r.id === id || r.id.startsWith(id));
				if (!review) {
					error = 'Review not found';
				} else {
					// Load changeset and comments in parallel
					const promises = [];
					if (review.targetId && review.targetKind === 'ChangeSet') {
						promises.push(loadChangeset(review.targetId));
					}
					promises.push(loadComments());
					await Promise.all(promises);
				}
			}
		} catch (e) {
			error = 'Failed to load review';
		}

		loading = false;
	}

	async function loadComments() {
		const { slug, repo, id } = $page.params;
		try {
			const data = await api('GET', `/${slug}/${repo}/v1/reviews/${id}/comments`);
			if (!data.error) {
				comments = data.comments || [];
			}
		} catch (e) {
			console.error('Failed to load comments', e);
		}
	}

	async function submitComment() {
		if (!newComment.trim()) return;

		commentLoading = true;
		const { slug, repo, id } = $page.params;

		try {
			const data = await api('POST', `/${slug}/${repo}/v1/reviews/${id}/comments`, {
				body: newComment,
				author: $currentUser?.email || 'anonymous'
			});
			if (!data.error) {
				comments = [...comments, data];
				newComment = '';
			}
		} catch (e) {
			console.error('Failed to submit comment', e);
		}

		commentLoading = false;
	}

	function openInlineComment(lineNum, lineType) {
		inlineCommentLine = { file: selectedFile, line: lineNum, type: lineType };
		inlineCommentText = '';
	}

	function cancelInlineComment() {
		inlineCommentLine = null;
		inlineCommentText = '';
	}

	async function submitInlineComment() {
		if (!inlineCommentText.trim() || !inlineCommentLine) return;

		commentLoading = true;
		const { slug, repo, id } = $page.params;

		try {
			const data = await api('POST', `/${slug}/${repo}/v1/reviews/${id}/comments`, {
				body: inlineCommentText,
				author: $currentUser?.email || 'anonymous',
				filePath: inlineCommentLine.file,
				line: inlineCommentLine.line
			});
			if (!data.error) {
				comments = [...comments, data];
				inlineCommentLine = null;
				inlineCommentText = '';
			}
		} catch (e) {
			console.error('Failed to submit inline comment', e);
		}

		commentLoading = false;
	}

	function getCommentsForLine(filePath, lineNum) {
		return comments.filter(c => c.filePath === filePath && c.line === lineNum && !c.parentId);
	}

	function getRepliesForComment(commentId) {
		return comments.filter(c => c.parentId === commentId);
	}

	function startReply(commentId) {
		replyingTo = commentId;
		replyText = '';
	}

	function cancelReply() {
		replyingTo = null;
		replyText = '';
	}

	async function submitReply() {
		if (!replyText.trim() || !replyingTo) return;

		commentLoading = true;
		const { slug, repo, id } = $page.params;

		try {
			const data = await api('POST', `/${slug}/${repo}/v1/reviews/${id}/comments`, {
				body: replyText,
				author: $currentUser?.email || 'anonymous',
				parentId: replyingTo
			});
			if (!data.error) {
				comments = [...comments, data];
				replyingTo = null;
				replyText = '';
			}
		} catch (e) {
			console.error('Failed to submit reply', e);
		}

		commentLoading = false;
	}

	async function loadChangeset(targetId) {
		const { slug, repo } = $page.params;
		try {
			const data = await api('GET', `/${slug}/${repo}/v1/changesets/${targetId}`);
			if (!data.error) {
				changeset = data;
			}
		} catch (e) {
			console.error('Failed to load changeset', e);
		}
	}

	function startEditingIntent() {
		intentDraft = changeset?.intent || '';
		editingIntent = true;
	}

	function cancelEditingIntent() {
		editingIntent = false;
		intentDraft = '';
	}

	async function saveIntent() {
		if (!changeset?.id) return;
		const { slug, repo } = $page.params;
		try {
			const data = await api('PATCH', `/${slug}/${repo}/v1/changesets/${changeset.id}`, {
				intent: intentDraft
			});
			if (!data.error) {
				changeset = { ...changeset, intent: intentDraft };
				editingIntent = false;
			}
		} catch (e) {
			console.error('Failed to save intent', e);
		}
	}

	function groupChanges(cs) {
		if (!cs?.files) return [];

		const groups = {
			api: { kind: 'feature', summary: 'API changes', files: [], symbols: [] },
			internal: { kind: 'refactor', summary: 'Internal changes', files: [], symbols: [] },
			test: { kind: 'test', summary: 'Test changes', files: [], symbols: [] },
			config: { kind: 'chore', summary: 'Configuration changes', files: [], symbols: [] },
			docs: { kind: 'docs', summary: 'Documentation changes', files: [], symbols: [] }
		};

		for (const file of cs.files) {
			const category = categorizeFile(file.path);
			if (!groups[category]) {
				groups[category] = { kind: 'chore', summary: 'Other changes', files: [], symbols: [] };
			}
			groups[category].files.push(file);

			// Add symbols from this file
			if (file.units) {
				for (const unit of file.units) {
					groups[category].symbols.push({
						...unit,
						file: file.path
					});
				}
			}
		}

		// Convert to array, filter empty groups
		const order = ['api', 'internal', 'test', 'config', 'docs'];
		return order
			.map(key => ({ key, ...groups[key] }))
			.filter(g => g.files.length > 0)
			.map(g => ({
				...g,
				summary: buildGroupSummary(g)
			}));
	}

	function categorizeFile(path) {
		const lower = path.toLowerCase();

		if (lower.includes('_test.') || lower.includes('.test.') ||
			lower.includes('/test/') || lower.includes('/tests/') ||
			lower.endsWith('_test.go') || lower.endsWith('.spec.ts')) {
			return 'test';
		}

		if (lower.endsWith('.md') || lower.endsWith('.txt') || lower.includes('/docs/')) {
			return 'docs';
		}

		if (lower.endsWith('.json') || lower.endsWith('.yaml') ||
			lower.endsWith('.yml') || lower.endsWith('.toml') ||
			lower.includes('config') || lower === 'package.json' ||
			lower === 'go.mod' || lower === 'go.sum') {
			return 'config';
		}

		if (lower.includes('/api/') || lower.includes('/handler') ||
			lower.includes('/route') || lower.includes('/endpoint') ||
			lower.includes('controller')) {
			return 'api';
		}

		return 'internal';
	}

	function buildGroupSummary(group) {
		const added = group.symbols.filter(s => s.action === 'added').length;
		const modified = group.symbols.filter(s => s.action === 'modified').length;
		const removed = group.symbols.filter(s => s.action === 'removed').length;

		const parts = [];
		if (added > 0) parts.push(`${added} added`);
		if (modified > 0) parts.push(`${modified} modified`);
		if (removed > 0) parts.push(`${removed} removed`);

		if (parts.length === 0) {
			return `${group.files.length} files changed`;
		}
		return `${group.files.length} files, ${parts.join(', ')}`;
	}

	function toggleGroup(key) {
		expandedGroups = { ...expandedGroups, [key]: !expandedGroups[key] };
	}

	function getStateColor(state) {
		switch (state) {
			case 'open':
				return 'bg-green-500/20 text-green-400';
			case 'approved':
				return 'bg-blue-500/20 text-blue-400';
			case 'changes_requested':
				return 'bg-yellow-500/20 text-yellow-400';
			case 'merged':
				return 'bg-purple-500/20 text-purple-400';
			case 'abandoned':
				return 'bg-gray-500/20 text-gray-400';
			default:
				return 'bg-gray-500/20 text-gray-400';
		}
	}

	function formatState(state) {
		return state?.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()) || 'Unknown';
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleString();
	}

	function getActionIcon(action) {
		switch (action) {
			case 'added': return '+';
			case 'removed': return '-';
			default: return '~';
		}
	}

	function getActionColor(action) {
		switch (action) {
			case 'added': return 'text-green-400';
			case 'removed': return 'text-red-400';
			default: return 'text-yellow-400';
		}
	}

	function getKindColor(kind) {
		switch (kind) {
			case 'feature': return 'text-green-400';
			case 'fix': return 'text-red-400';
			case 'test': return 'text-blue-400';
			case 'docs': return 'text-purple-400';
			default: return 'text-kai-text-muted';
		}
	}

	async function updateState(newState) {
		const { slug, repo, id } = $page.params;
		const data = await api('POST', `/${slug}/${repo}/v1/reviews/${id}/state`, { state: newState });
		if (!data.error) {
			review = { ...review, state: newState };
		}
	}

	async function loadFileDiff(filePath) {
		if (!changeset?.base || !changeset?.head) {
			return;
		}

		selectedFile = filePath;
		diffLoading = true;
		fileDiff = null;
		semanticDiff = null;

		const { slug, repo } = $page.params;

		// Load both line diff and semantic diff in parallel
		try {
			const encodedPath = encodeURIComponent(filePath);
			const [lineData, semanticData] = await Promise.all([
				api('GET', `/${slug}/${repo}/v1/diff/${changeset.base}/${changeset.head}?path=${encodedPath}`),
				api('GET', `/${slug}/${repo}/v1/semantic-diff/${changeset.id}?path=${encodedPath}`)
			]);

			if (!lineData.error) {
				fileDiff = lineData;
			}
			if (!semanticData.error) {
				semanticDiff = semanticData;
			}
		} catch (e) {
			console.error('Failed to load diff', e);
		}

		diffLoading = false;
	}

	function closeFileDiff() {
		selectedFile = null;
		fileDiff = null;
		semanticDiff = null;
	}

	function toggleDiffMode() {
		diffMode = diffMode === 'line' ? 'semantic' : 'line';
	}

	function getUnitIcon(kind) {
		switch (kind?.toLowerCase()) {
			case 'function': return 'fn';
			case 'method': return 'M';
			case 'class': return 'C';
			case 'struct': return 'S';
			case 'const': return 'c';
			case 'var': return 'v';
			case 'type': return 'T';
			case 'import': return 'I';
			default: return '?';
		}
	}

	function getUnitColor(kind) {
		switch (kind?.toLowerCase()) {
			case 'function':
			case 'method':
				return 'text-blue-400';
			case 'class':
			case 'struct':
				return 'text-yellow-400';
			case 'const':
			case 'var':
				return 'text-purple-400';
			case 'type':
				return 'text-green-400';
			default:
				return 'text-kai-text-muted';
		}
	}

	function parseDiffLines(diff) {
		if (!diff) return [];
		const lines = diff.split('\n');
		return lines.map((line, i) => {
			let type = 'context';
			if (line.startsWith('+') && !line.startsWith('+++')) {
				type = 'addition';
			} else if (line.startsWith('-') && !line.startsWith('---')) {
				type = 'deletion';
			} else if (line.startsWith('@@')) {
				type = 'hunk';
			} else if (line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++')) {
				type = 'header';
			}
			return { line, type, number: i + 1 };
		});
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<!-- Breadcrumb -->
	<nav class="text-sm text-kai-text-muted mb-4">
		<a href="/orgs/{$page.params.slug}" class="hover:text-kai-text">{$page.params.slug}</a>
		<span class="mx-2">/</span>
		<a href="/orgs/{$page.params.slug}/{$page.params.repo}" class="hover:text-kai-text">{$page.params.repo}</a>
		<span class="mx-2">/</span>
		<a href="/orgs/{$page.params.slug}/{$page.params.repo}/reviews" class="hover:text-kai-text">Reviews</a>
		<span class="mx-2">/</span>
		<span>{$page.params.id.slice(0, 8)}</span>
	</nav>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-400 mb-4">{error}</p>
			<a href="/orgs/{$page.params.slug}/{$page.params.repo}/reviews" class="btn">Back to Reviews</a>
		</div>
	{:else if review}
		<!-- Header -->
		<div class="mb-6">
			<div class="flex items-center gap-3 mb-2">
				<span class="px-2 py-1 rounded text-sm font-medium {getStateColor(review.state)}">
					{formatState(review.state)}
				</span>
				<h1 class="text-2xl font-semibold">{review.title || 'Untitled Review'}</h1>
			</div>
			{#if review.description}
				<p class="text-kai-text-muted mt-2">{review.description}</p>
			{/if}
			<div class="text-sm text-kai-text-muted mt-2 flex items-center gap-4">
				<span>by {review.author || 'unknown'}</span>
				{#if review.createdAt}
					<span>{formatDate(review.createdAt)}</span>
				{/if}
			</div>
		</div>

		<!-- Intent Summary -->
		{#if changeset}
			<div class="mb-6 p-4 bg-kai-bg-secondary border border-kai-border rounded-lg">
				<div class="flex items-start justify-between gap-4">
					<div class="flex-1">
						<div class="flex items-center gap-2 mb-2">
							<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
								<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
							</svg>
							<span class="text-sm font-medium text-kai-text-muted">What changed</span>
						</div>
						{#if editingIntent}
							<div class="flex gap-2">
								<input
									type="text"
									class="input flex-1"
									bind:value={intentDraft}
									placeholder="Describe what this change does..."
									onkeydown={(e) => e.key === 'Enter' && saveIntent()}
								/>
								<button class="btn btn-primary btn-sm" onclick={saveIntent}>Save</button>
								<button class="btn btn-sm" onclick={cancelEditingIntent}>Cancel</button>
							</div>
						{:else}
							<p class="text-lg">
								{changeset.intent || 'No description'}
								<button
									class="ml-2 text-kai-text-muted hover:text-kai-text text-sm"
									onclick={startEditingIntent}
									title="Edit intent"
								>
									<svg class="w-4 h-4 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
									</svg>
								</button>
							</p>
						{/if}
					</div>
				</div>
			</div>
		{/if}

		<!-- Actions -->
		{#if review.state === 'open' || review.state === 'draft'}
			<div class="flex gap-2 mb-6">
				<button class="btn btn-primary" onclick={() => updateState('approved')}>Approve</button>
				<button class="btn" onclick={() => updateState('changes_requested')}>Request Changes</button>
				{#if review.state === 'draft'}
					<button class="btn" onclick={() => updateState('open')}>Mark Ready</button>
				{/if}
			</div>
		{/if}

		<!-- Progressive Disclosure: Level 1 - What Changed -->
		<div class="card mb-6">
			<h2 class="text-lg font-semibold mb-4">Changes</h2>

			{#if changeGroups.length === 0}
				<p class="text-kai-text-muted">No changes found</p>
			{:else}
				<div class="space-y-2">
					{#each changeGroups as group, i}
						<div class="border border-kai-border rounded-lg overflow-hidden">
							<!-- Group Header (clickable) -->
							<button
								class="w-full px-4 py-3 flex items-center justify-between hover:bg-kai-bg-tertiary transition-colors text-left"
								onclick={() => toggleGroup(group.key)}
							>
								<div class="flex items-center gap-3">
									<span class="w-6 h-6 rounded bg-kai-bg-tertiary flex items-center justify-center text-sm font-medium">
										{i + 1}
									</span>
									<span class="font-medium">{group.summary}</span>
									<span class="text-xs px-2 py-0.5 rounded {getKindColor(group.kind)} bg-kai-bg-tertiary">
										{group.kind}
									</span>
								</div>
								<svg
									class="w-5 h-5 text-kai-text-muted transition-transform {expandedGroups[group.key] ? 'rotate-180' : ''}"
									fill="none"
									stroke="currentColor"
									viewBox="0 0 24 24"
								>
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
								</svg>
							</button>

							<!-- Group Details (Level 2) -->
							{#if expandedGroups[group.key]}
								<div class="border-t border-kai-border px-4 py-3 bg-kai-bg">
									<!-- Files -->
									<div class="mb-4">
										<h4 class="text-sm font-medium text-kai-text-muted mb-2">Files</h4>
										<ul class="space-y-1">
											{#each group.files as file}
												<li class="text-sm font-mono">
													<button
														class="flex items-center gap-2 hover:text-blue-400 hover:underline cursor-pointer transition-colors text-left w-full {selectedFile === file.path ? 'text-blue-400' : ''}"
														onclick={() => loadFileDiff(file.path)}
													>
														<span class="text-kai-text-muted">•</span>
														<span class="hover:underline">{file.path}</span>
													</button>
												</li>
											{/each}
										</ul>
									</div>

									<!-- Symbols -->
									{#if group.symbols.length > 0}
										<div>
											<h4 class="text-sm font-medium text-kai-text-muted mb-2">Symbols</h4>
											<ul class="space-y-1">
												{#each group.symbols as sym}
													<li class="text-sm font-mono flex items-center gap-2">
														<span class="{getActionColor(sym.action)} font-bold w-4">
															{getActionIcon(sym.action)}
														</span>
														<span class="text-kai-text-muted">{sym.kind}</span>
														<span>{sym.name}</span>
														{#if sym.signature}
															<span class="text-kai-text-muted">→ {sym.signature}</span>
														{/if}
													</li>
												{/each}
											</ul>
										</div>
									{/if}
								</div>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<!-- File Diff Viewer -->
		{#if selectedFile}
			<div class="card mb-6">
				<div class="flex items-center justify-between mb-4">
					<div class="flex items-center gap-3">
						<h2 class="text-lg font-semibold font-mono">{selectedFile}</h2>
						<!-- Diff Mode Toggle -->
						<div class="flex rounded-lg bg-kai-bg-tertiary p-1">
							<button
								class="px-3 py-1 text-xs font-medium rounded transition-colors {diffMode === 'semantic' ? 'bg-kai-bg text-kai-text' : 'text-kai-text-muted hover:text-kai-text'}"
								onclick={() => diffMode = 'semantic'}
							>
								Semantic
							</button>
							<button
								class="px-3 py-1 text-xs font-medium rounded transition-colors {diffMode === 'line' ? 'bg-kai-bg text-kai-text' : 'text-kai-text-muted hover:text-kai-text'}"
								onclick={() => diffMode = 'line'}
							>
								Lines
							</button>
						</div>
					</div>
					<button
						class="text-kai-text-muted hover:text-kai-text transition-colors"
						onclick={closeFileDiff}
					>
						<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
						</svg>
					</button>
				</div>

				{#if diffLoading}
					<div class="text-center py-8 text-kai-text-muted">Loading diff...</div>
				{:else if diffMode === 'semantic'}
					<!-- Semantic Diff View -->
					{#if semanticDiff?.units && semanticDiff.units.length > 0}
						<div class="space-y-2">
							{#each semanticDiff.units as unit}
								<div class="flex items-start gap-3 p-3 rounded-lg bg-kai-bg border border-kai-border">
									<span class="flex-shrink-0 w-8 h-8 rounded flex items-center justify-center text-xs font-bold {getUnitColor(unit.kind)} bg-kai-bg-tertiary">
										{getUnitIcon(unit.kind)}
									</span>
									<div class="flex-1 min-w-0">
										<div class="flex items-center gap-2 mb-1">
											<span class="font-mono font-medium">{unit.name}</span>
											<span class="text-xs px-2 py-0.5 rounded {unit.action === 'added' ? 'bg-green-500/20 text-green-400' : unit.action === 'removed' ? 'bg-red-500/20 text-red-400' : 'bg-yellow-500/20 text-yellow-400'}">
												{unit.action}
											</span>
											{#if unit.changeType}
												<span class="text-xs text-kai-text-muted">{unit.changeType.replace(/_/g, ' ').toLowerCase()}</span>
											{/if}
										</div>
										{#if unit.fqName && unit.fqName !== unit.name}
											<div class="text-xs text-kai-text-muted font-mono">{unit.fqName}</div>
										{/if}
										{#if unit.beforeSig || unit.afterSig}
											<div class="mt-2 text-xs font-mono">
												{#if unit.beforeSig && unit.action !== 'added'}
													<div class="text-red-400/70">- {unit.beforeSig}</div>
												{/if}
												{#if unit.afterSig && unit.action !== 'removed'}
													<div class="text-green-400/70">+ {unit.afterSig}</div>
												{/if}
											</div>
										{/if}
									</div>
								</div>
							{/each}
						</div>
					{:else}
						<div class="text-center py-8 text-kai-text-muted">No semantic changes detected</div>
					{/if}
				{:else if fileDiff?.hunks && fileDiff.hunks.length > 0}
					<!-- Line Diff View -->
					<div class="bg-kai-bg rounded-lg overflow-x-auto">
						{#each fileDiff.hunks as hunk, hunkIdx}
							<div class="border-b border-kai-border last:border-b-0">
								<div class="bg-blue-900/20 text-blue-300 px-4 py-1 text-xs font-mono">
									@@ -{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
								</div>
								<div class="text-sm font-mono">
									{#each hunk.lines as line}
										{@const lineNum = line.newLine || line.oldLine}
										{@const lineComments = getCommentsForLine(selectedFile, lineNum)}
										<!-- Line row -->
										<div
											class="group flex items-stretch hover:bg-kai-bg-tertiary/50 cursor-pointer {line.type === 'add' ? 'bg-green-900/30' : line.type === 'delete' ? 'bg-red-900/30' : ''}"
											onclick={() => openInlineComment(lineNum, line.type)}
										>
											<span class="w-12 flex-shrink-0 text-right pr-2 text-kai-text-muted text-xs py-0.5 select-none border-r border-kai-border">
												{lineNum || ''}
											</span>
											<span class="flex-shrink-0 w-5 text-center py-0.5 {line.type === 'add' ? 'text-green-300' : line.type === 'delete' ? 'text-red-300' : 'text-kai-text-muted'}">
												{line.type === 'add' ? '+' : line.type === 'delete' ? '-' : ' '}
											</span>
											<code class="flex-1 py-0.5 pr-4 hljs {line.type === 'add' ? 'bg-green-900/20' : line.type === 'delete' ? 'bg-red-900/20' : ''}">{@html highlightLine(line.content, selectedFile)}</code>
											<span class="w-8 flex-shrink-0 flex items-center justify-center opacity-0 group-hover:opacity-100 text-blue-400" title="Add comment">
												+
											</span>
										</div>
										<!-- Existing comments on this line -->
										{#each lineComments as comment}
											<div class="ml-12 mr-4 my-2 p-3 bg-kai-bg-tertiary rounded-lg border border-kai-border">
												<div class="flex items-center justify-between mb-1">
													<span class="text-xs font-medium">{comment.author || 'Anonymous'}</span>
													<span class="text-xs text-kai-text-muted">{formatDate(comment.createdAt)}</span>
												</div>
												<div class="text-sm prose prose-invert prose-sm max-w-none">{@html renderMarkdown(comment.body, $page.params.slug)}</div>
											</div>
										{/each}
										<!-- Inline comment form -->
										{#if inlineCommentLine?.file === selectedFile && inlineCommentLine?.line === lineNum}
											<div class="ml-12 mr-4 my-2 p-3 bg-kai-bg-tertiary rounded-lg border border-blue-500">
												<textarea
													bind:value={inlineCommentText}
													placeholder="Add a comment on this line..."
													class="w-full px-2 py-1 bg-kai-bg border border-kai-border rounded text-sm focus:outline-none focus:border-kai-accent resize-none"
													rows="2"
												></textarea>
												<div class="flex justify-end gap-2 mt-2">
													<button class="btn btn-secondary btn-sm" onclick={cancelInlineComment}>Cancel</button>
													<button
														class="btn btn-primary btn-sm"
														onclick={submitInlineComment}
														disabled={commentLoading || !inlineCommentText.trim()}
													>
														{commentLoading ? 'Posting...' : 'Comment'}
													</button>
												</div>
											</div>
										{/if}
									{/each}
								</div>
							</div>
						{/each}
					</div>
				{:else if fileDiff?.error}
					<div class="text-center py-8 text-red-400">{fileDiff.error}</div>
				{:else}
					<div class="text-center py-8 text-kai-text-muted">No diff available</div>
				{/if}
			</div>
		{/if}

		<!-- AI Suggestions (Level 3) -->
		{#if aiSuggestions.length > 0}
			<div class="card mb-6">
				<h2 class="text-lg font-semibold mb-4">AI Suggestions</h2>
				<div class="space-y-2">
					{#each aiSuggestions as suggestion}
						<div class="flex items-start gap-3 p-3 rounded-lg bg-kai-bg">
							<span class="text-lg">
								{#if suggestion.level === 'error'}
									<span class="text-red-400">✗</span>
								{:else if suggestion.level === 'warning'}
									<span class="text-yellow-400">⚠</span>
								{:else}
									<span class="text-blue-400">•</span>
								{/if}
							</span>
							<div class="flex-1">
								<div class="flex items-center gap-2 mb-1">
									<span class="text-xs px-2 py-0.5 rounded bg-kai-bg-tertiary text-kai-text-muted">
										{suggestion.category}
									</span>
									{#if suggestion.file}
										<span class="text-xs text-kai-text-muted font-mono">{suggestion.file}</span>
									{/if}
								</div>
								<p class="text-sm">{suggestion.message}</p>
							</div>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<!-- Comments -->
		<div class="card mb-6">
			<h2 class="text-lg font-semibold mb-4">Comments ({comments.length})</h2>

			{#if comments.length > 0}
				<div class="space-y-4 mb-6">
					{#each commentThreads() as thread}
						<!-- Top-level comment -->
						<div class="border border-kai-border rounded-lg overflow-hidden">
							<div class="p-4">
								<div class="flex items-center justify-between mb-2">
									<span class="font-medium text-sm">{thread.author || 'Anonymous'}</span>
									<span class="text-xs text-kai-text-muted">
										{thread.createdAt ? formatDate(thread.createdAt) : ''}
									</span>
								</div>
								<div class="text-sm prose prose-invert prose-sm max-w-none">{@html renderMarkdown(thread.body, $page.params.slug)}</div>
								{#if thread.filePath}
									<div class="mt-2 text-xs text-kai-text-muted font-mono">
										{thread.filePath}{thread.line ? `:${thread.line}` : ''}
									</div>
								{/if}
								<button
									class="mt-2 text-xs text-blue-400 hover:text-blue-300"
									onclick={() => startReply(thread.id)}
								>
									Reply
								</button>
							</div>

							<!-- Replies -->
							{#if thread.replies.length > 0}
								<div class="border-t border-kai-border bg-kai-bg-tertiary/30">
									{#each thread.replies as reply}
										<div class="p-4 pl-8 border-b border-kai-border/50 last:border-b-0">
											<div class="flex items-center justify-between mb-2">
												<span class="font-medium text-sm">{reply.author || 'Anonymous'}</span>
												<span class="text-xs text-kai-text-muted">
													{reply.createdAt ? formatDate(reply.createdAt) : ''}
												</span>
											</div>
											<div class="text-sm prose prose-invert prose-sm max-w-none">{@html renderMarkdown(reply.body, $page.params.slug)}</div>
										</div>
									{/each}
								</div>
							{/if}

							<!-- Reply form -->
							{#if replyingTo === thread.id}
								<div class="p-4 border-t border-kai-border bg-kai-bg-tertiary/30">
									<textarea
										bind:value={replyText}
										placeholder="Write a reply..."
										class="w-full px-3 py-2 bg-kai-bg border border-kai-border rounded text-sm focus:outline-none focus:border-kai-accent resize-none"
										rows="2"
									></textarea>
									<div class="flex justify-end gap-2 mt-2">
										<button class="btn btn-secondary btn-sm" onclick={cancelReply}>Cancel</button>
										<button
											class="btn btn-primary btn-sm"
											onclick={submitReply}
											disabled={commentLoading || !replyText.trim()}
										>
											{commentLoading ? 'Posting...' : 'Reply'}
										</button>
									</div>
								</div>
							{/if}
						</div>
					{/each}
				</div>
			{/if}

			<!-- Add comment form -->
			<div class="border-t border-kai-border pt-4">
				<textarea
					bind:value={newComment}
					placeholder="Add a comment..."
					class="w-full px-3 py-2 bg-kai-bg border border-kai-border rounded-lg text-sm focus:outline-none focus:border-kai-accent resize-none"
					rows="3"
				></textarea>
				<div class="flex justify-end mt-2">
					<button
						class="btn btn-primary btn-sm"
						onclick={submitComment}
						disabled={commentLoading || !newComment.trim()}
					>
						{commentLoading ? 'Posting...' : 'Comment'}
					</button>
				</div>
			</div>
		</div>

		<!-- Metadata -->
		<div class="card">
			<h2 class="text-lg font-semibold mb-4">Details</h2>
			<dl class="grid grid-cols-2 gap-4 text-sm">
				<div>
					<dt class="text-kai-text-muted">Review ID</dt>
					<dd class="font-mono">{review.id}</dd>
				</div>
				<div>
					<dt class="text-kai-text-muted">Target</dt>
					<dd class="font-mono">{review.targetId?.slice(0, 12) || '-'}</dd>
				</div>
				<div>
					<dt class="text-kai-text-muted">Target Kind</dt>
					<dd>{review.targetKind || '-'}</dd>
				</div>
				<div>
					<dt class="text-kai-text-muted">Reviewers</dt>
					<dd>{review.reviewers?.join(', ') || 'None'}</dd>
				</div>
			</dl>
		</div>
	{/if}
</div>
