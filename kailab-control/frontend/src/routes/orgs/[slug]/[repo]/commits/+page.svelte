<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let entries = $state([]);
	let loading = $state(true);
	let error = $state('');

	$effect(() => {
		// Re-run when page params change
		$page.params.slug;
		$page.params.repo;
	});

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}
		await loadEntries();
	});

	async function loadEntries() {
		loading = true;
		error = '';
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/${slug}/${repo}/v1/log/entries?limit=50`);
			if (data.error) {
				error = data.error;
				entries = [];
			} else {
				entries = data.entries || [];
			}
		} catch (e) {
			error = 'Failed to load history';
			entries = [];
		}

		loading = false;
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleDateString('en-US', {
			month: 'short',
			day: 'numeric',
			year: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function formatRelativeTime(timestamp) {
		if (!timestamp) return '';
		const now = Date.now();
		const diff = now - timestamp;
		const seconds = Math.floor(diff / 1000);
		const minutes = Math.floor(seconds / 60);
		const hours = Math.floor(minutes / 60);
		const days = Math.floor(hours / 24);

		if (days > 7) {
			return formatDate(timestamp);
		} else if (days > 0) {
			return `${days}d ago`;
		} else if (hours > 0) {
			return `${hours}h ago`;
		} else if (minutes > 0) {
			return `${minutes}m ago`;
		} else {
			return 'just now';
		}
	}

	function hexEncode(bytes) {
		if (!bytes) return '';
		// bytes is a base64-encoded string from JSON
		try {
			const decoded = atob(bytes);
			return Array.from(decoded, c => c.charCodeAt(0).toString(16).padStart(2, '0')).join('');
		} catch {
			return bytes;
		}
	}

	function shortId(bytes) {
		const hex = hexEncode(bytes);
		return hex.slice(0, 8);
	}

	function getKindColor(kind) {
		switch (kind) {
			case 'REF_UPDATE':
				return 'bg-blue-500/20 text-blue-400';
			case 'NODE_PUBLISH':
				return 'bg-green-500/20 text-green-400';
			default:
				return 'bg-gray-500/20 text-gray-400';
		}
	}

	function getRefIcon(refName) {
		if (!refName) return '';
		if (refName.startsWith('snap.')) return 'snapshot';
		if (refName.startsWith('cs.')) return 'changeset';
		if (refName.startsWith('review.')) return 'review';
		if (refName.startsWith('ws.')) return 'workspace';
		return 'ref';
	}

	function formatRefName(refName) {
		if (!refName) return '';
		// Make ref names more readable
		if (refName.startsWith('snap.')) return refName.replace('snap.', 'snapshot/');
		if (refName.startsWith('cs.')) return refName.replace('cs.', 'changeset/');
		if (refName.startsWith('review.')) return refName.replace('review.', 'review/');
		if (refName.startsWith('ws.')) return refName.replace('ws.', 'workspace/');
		return refName;
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<div>
			<nav class="text-sm text-kai-text-muted mb-2">
				<a href="/orgs/{$page.params.slug}" class="hover:text-kai-text">{$page.params.slug}</a>
				<span class="mx-2">/</span>
				<a href="/orgs/{$page.params.slug}/{$page.params.repo}" class="hover:text-kai-text"
					>{$page.params.repo}</a
				>
				<span class="mx-2">/</span>
				<span>History</span>
			</nav>
			<h2 class="text-xl font-semibold">Repository History</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				Append-only log of all changes to this repository
			</p>
		</div>
		<div class="flex gap-2">
			<a href="/orgs/{$page.params.slug}/{$page.params.repo}/reviews" class="btn btn-secondary">
				Reviews
			</a>
		</div>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-400 mb-4">{error}</p>
			<button class="btn" onclick={loadEntries}>Retry</button>
		</div>
	{:else if entries.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">📜</div>
			<p class="text-kai-text-muted mb-4">No history yet</p>
			<p class="text-kai-text-muted text-sm">
				Push changes with <code class="bg-kai-bg-tertiary px-2 py-1 rounded">kai push</code>
			</p>
		</div>
	{:else}
		<div class="card p-0">
			{#each entries as entry, i}
				<div
					class="list-item {i < entries.length - 1 ? 'border-b border-kai-border' : ''}"
				>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-3">
							<span class="px-2 py-0.5 rounded text-xs font-medium {getKindColor(entry.kind)}">
								{entry.kind === 'REF_UPDATE' ? 'Update' : entry.kind}
							</span>
							<span class="font-mono text-sm text-kai-text-muted">
								{formatRefName(entry.ref)}
							</span>
						</div>
						<div class="text-kai-text-muted text-xs mt-2 flex items-center gap-4">
							<span class="font-mono" title={hexEncode(entry.id)}>
								{shortId(entry.id)}
							</span>
							<span>by {entry.actor || 'unknown'}</span>
							<span title={formatDate(entry.time)}>
								{formatRelativeTime(entry.time)}
							</span>
						</div>
						{#if entry.old || entry.new}
							<div class="text-xs mt-2 font-mono flex items-center gap-2">
								{#if entry.old}
									<span class="text-red-400/70" title={hexEncode(entry.old)}>
										-{shortId(entry.old)}
									</span>
								{/if}
								{#if entry.old && entry.new}
									<span class="text-kai-text-muted">→</span>
								{/if}
								{#if entry.new}
									<span class="text-green-400/70" title={hexEncode(entry.new)}>
										+{shortId(entry.new)}
									</span>
								{/if}
							</div>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>
