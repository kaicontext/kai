<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let review = $state(null);
	let changeset = $state(null);
	let loading = $state(true);
	let error = $state('');
	let expandedGroups = $state({});
	let aiSuggestions = $state([]);
	let aiLoading = $state(false);

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
				} else if (review.targetId && review.targetKind === 'ChangeSet') {
					// Load changeset details
					await loadChangeset(review.targetId);
				}
			}
		} catch (e) {
			error = 'Failed to load review';
		}

		loading = false;
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
												<li class="text-sm font-mono flex items-center gap-2">
													<span class="text-kai-text-muted">•</span>
													{file.path}
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
