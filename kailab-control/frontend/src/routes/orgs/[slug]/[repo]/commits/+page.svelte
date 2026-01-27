<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let entries = $state([]);
	let loading = $state(true);
	let error = $state('');
	let changesetCache = $state({});

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
				// Load changeset details for entries that reference changesets
				await loadChangesetDetails();
			}
		} catch (e) {
			error = 'Failed to load history';
			entries = [];
		}

		loading = false;
	}

	async function loadChangesetDetails() {
		const { slug, repo } = $page.params;

		// Find unique changeset IDs from cs.* ref updates
		const changesetIds = new Set();
		for (const entry of entries) {
			if (entry.ref && entry.ref.startsWith('cs.') && entry.new) {
				const newHex = hexEncode(entry.new);
				if (newHex && !changesetCache[newHex]) {
					changesetIds.add(newHex);
				}
			}
		}

		// Fetch changeset details in parallel
		const promises = Array.from(changesetIds).map(async (id) => {
			try {
				const data = await api('GET', `/${slug}/${repo}/v1/changesets/${id}`);
				if (!data.error) {
					changesetCache[id] = data;
				}
			} catch (e) {
				// Ignore errors for individual changesets
			}
		});

		await Promise.all(promises);
		// Trigger reactivity
		changesetCache = { ...changesetCache };
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

	function getRefType(refName) {
		if (!refName) return 'ref';
		if (refName.startsWith('snap.')) return 'snapshot';
		if (refName.startsWith('cs.')) return 'changeset';
		if (refName.startsWith('review.')) return 'review';
		if (refName.startsWith('ws.')) return 'workspace';
		return 'ref';
	}

	function formatRefName(refName) {
		if (!refName) return '';
		// Make ref names more readable
		if (refName.startsWith('snap.')) return refName.replace('snap.', '');
		if (refName.startsWith('cs.')) return refName.replace('cs.', '');
		if (refName.startsWith('review.')) return refName.replace('review.', '');
		if (refName.startsWith('ws.')) return refName.replace('ws.', '');
		return refName;
	}

	function getRefBadgeColor(refName) {
		const type = getRefType(refName);
		switch (type) {
			case 'snapshot':
				return 'bg-purple-500/20 text-purple-400';
			case 'changeset':
				return 'bg-green-500/20 text-green-400';
			case 'review':
				return 'bg-yellow-500/20 text-yellow-400';
			case 'workspace':
				return 'bg-blue-500/20 text-blue-400';
			default:
				return 'bg-gray-500/20 text-gray-400';
		}
	}

	function getCommitMessage(entry) {
		// Check if we have changeset details with intent
		if (entry.ref && entry.ref.startsWith('cs.') && entry.new) {
			const newHex = hexEncode(entry.new);
			const cs = changesetCache[newHex];
			if (cs && cs.intent) {
				return cs.intent;
			}
		}
		// Check meta field
		if (entry.meta && entry.meta.message) {
			return entry.meta.message;
		}
		return null;
	}

	function getChangesetForEntry(entry) {
		if (entry.ref && entry.ref.startsWith('cs.') && entry.new) {
			const newHex = hexEncode(entry.new);
			return changesetCache[newHex];
		}
		// For snapshot updates, try to find matching changeset
		if (entry.ref && entry.ref.startsWith('snap.') && entry.new) {
			// Look through cache for changeset with matching head
			const newHex = hexEncode(entry.new);
			for (const [id, cs] of Object.entries(changesetCache)) {
				if (cs.head === newHex) {
					return cs;
				}
			}
		}
		return null;
	}

	function viewDiff(entry) {
		const cs = getChangesetForEntry(entry);
		if (cs && cs.base && cs.head) {
			goto(`/orgs/${$page.params.slug}/${$page.params.repo}?diff=${cs.base}..${cs.head}`);
		} else if (entry.old && entry.new) {
			// Fallback: use old/new from log entry for snapshot refs
			const oldHex = hexEncode(entry.old);
			const newHex = hexEncode(entry.new);
			goto(`/orgs/${$page.params.slug}/${$page.params.repo}?diff=${oldHex}..${newHex}`);
		}
	}

	function canViewDiff(entry) {
		const cs = getChangesetForEntry(entry);
		if (cs && cs.base && cs.head) return true;
		// For snapshot updates, we can diff old vs new
		if (entry.ref && entry.ref.startsWith('snap.') && entry.old && entry.new) return true;
		return false;
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
				{@const message = getCommitMessage(entry)}
				{@const changeset = getChangesetForEntry(entry)}
				<div
					class="list-item {i < entries.length - 1 ? 'border-b border-kai-border' : ''}"
				>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-3">
							<span class="px-2 py-0.5 rounded text-xs font-medium {getRefBadgeColor(entry.ref)}">
								{getRefType(entry.ref)}
							</span>
							<span class="font-mono text-sm text-kai-text-muted">
								{formatRefName(entry.ref)}
							</span>
						</div>

						{#if message}
							<div class="mt-2 text-sm font-medium">
								{message}
							</div>
						{/if}

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
										{shortId(entry.old)}
									</span>
								{/if}
								{#if entry.old && entry.new}
									<span class="text-kai-text-muted">→</span>
								{/if}
								{#if entry.new}
									<span class="text-green-400/70" title={hexEncode(entry.new)}>
										{shortId(entry.new)}
									</span>
								{/if}
							</div>
						{/if}
					</div>

					<div class="flex items-center gap-2">
						{#if canViewDiff(entry)}
							<button
								class="btn btn-secondary btn-sm"
								onclick={() => viewDiff(entry)}
							>
								View Diff
							</button>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>
