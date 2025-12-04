<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser, accessToken, currentOrg, currentRepo } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let repo = $state(null);
	let refs = $state([]);
	let loading = $state(true);
	let refsLoading = $state(true);
	let activeTab = $state('snapshots');

	$effect(() => {
		currentOrg.set($page.params.slug);
		currentRepo.set($page.params.repo);
	});

	onMount(async () => {
		if (!$accessToken) {
			goto('/login');
			return;
		}

		if (!$currentUser) {
			await loadUser();
		}

		await loadRepo();
		await loadRefs();
	});

	async function loadRepo() {
		loading = true;
		const data = await api('GET', `/api/v1/orgs/${$page.params.slug}/repos/${$page.params.repo}`);
		if (data.error) {
			goto(`/orgs/${$page.params.slug}`);
			return;
		}
		repo = data;
		loading = false;
	}

	async function loadRefs() {
		refsLoading = true;
		// Fetch refs from the data plane via proxy
		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/refs`);
		if (data.refs) {
			refs = data.refs;
		}
		refsLoading = false;
	}

	function getCloneUrl() {
		return `${window.location.origin}/${$page.params.slug}/${$page.params.repo}`;
	}

	function getQuickstart() {
		const cloneUrl = getCloneUrl();
		return `# Set up remote
kai remote set origin ${cloneUrl}

# Login (if not already)
kai auth login

# Push your latest snapshot
kai push origin snap.latest`;
	}

	function formatDate(timestamp) {
		if (!timestamp) return '-';
		// Timestamp is in milliseconds if > year 2100 in seconds
		const ms = timestamp > 4102444800 ? timestamp : timestamp * 1000;
		return new Date(ms).toLocaleString();
	}

	function shortHash(target) {
		if (!target) return '-';
		// target is base64 encoded, decode and show first 12 hex chars
		try {
			const bytes = atob(target);
			let hex = '';
			for (let i = 0; i < Math.min(bytes.length, 6); i++) {
				hex += bytes.charCodeAt(i).toString(16).padStart(2, '0');
			}
			return hex;
		} catch {
			return target.substring(0, 12);
		}
	}

	function getRefType(name) {
		if (name.startsWith('snap.')) return 'snapshot';
		if (name.startsWith('cs.')) return 'changeset';
		if (name.startsWith('ws.')) return 'workspace';
		return 'ref';
	}

	function getRefIcon(name) {
		if (name.startsWith('snap.')) return '📸';
		if (name.startsWith('cs.')) return '📝';
		if (name.startsWith('ws.')) return '🔀';
		return '🏷️';
	}

	// Filter refs by type
	let snapshots = $derived(refs.filter(r => r.name.startsWith('snap.')));
	let changesets = $derived(refs.filter(r => r.name.startsWith('cs.')));
	let workspaces = $derived(refs.filter(r => r.name.startsWith('ws.')));
	let otherRefs = $derived(refs.filter(r => !r.name.startsWith('snap.') && !r.name.startsWith('cs.') && !r.name.startsWith('ws.')));
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	{#if loading || refsLoading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if repo}
		<div class="mb-6">
			<a href="/orgs/{$page.params.slug}" class="text-kai-accent hover:underline">
				← Back to {$page.params.slug}
			</a>
		</div>

		<div class="flex justify-between items-start mb-6">
			<div>
				<h2 class="text-xl font-semibold">
					{$page.params.slug}/{$page.params.repo}
					<span class="badge badge-{repo.visibility} ml-2">{repo.visibility}</span>
				</h2>
			</div>
		</div>

		<!-- Empty state - GitHub/GitLab style setup instructions -->
		{#if refs.length === 0}
			<div class="border border-kai-border rounded-md">
				<!-- Quick setup header -->
				<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border">
					<h3 class="font-semibold">Quick setup</h3>
				</div>

				<div class="p-4 border-b border-kai-border">
					<div class="flex items-center gap-2 mb-2">
						<span class="text-kai-text-muted text-sm">Remote URL:</span>
					</div>
					<div class="flex gap-2 items-center">
						<input type="text" readonly value={getCloneUrl()} class="input flex-1 font-mono text-sm bg-kai-bg" />
						<button
							class="btn"
							onclick={() => {
								navigator.clipboard.writeText(getCloneUrl());
							}}
						>
							Copy
						</button>
					</div>
				</div>

				<!-- Push an existing repository -->
				<div class="p-4">
					<h4 class="font-medium mb-3">…or push from an existing Kai repository</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">kai remote set origin {getCloneUrl()}
kai auth login
kai push origin snap.latest</pre>
					</div>
				</div>

				<!-- Create new from command line -->
				<div class="p-4 border-t border-kai-border">
					<h4 class="font-medium mb-3">…or create a new snapshot from command line</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">cd your-project
kai init
kai snapshot main --repo .
kai analyze symbols @snap:last
kai remote set origin {getCloneUrl()}
kai auth login
kai push origin snap.latest</pre>
					</div>
				</div>
			</div>

		<!-- Has content - show tabs with snapshots/changesets -->
		{:else}
			<!-- Tabs -->
			<div class="border-b border-kai-border mb-6">
				<nav class="flex gap-4">
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'snapshots' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => activeTab = 'snapshots'}
					>
						Snapshots
						{#if snapshots.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{snapshots.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'changesets' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => activeTab = 'changesets'}
					>
						Changesets
						{#if changesets.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{changesets.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'setup' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => activeTab = 'setup'}
					>
						Setup
					</button>
				</nav>
			</div>

			<!-- Tab Content -->
			{#if activeTab === 'snapshots'}
				{#if snapshots.length === 0}
					<div class="text-center py-8 text-kai-text-muted">
						<p>No snapshot refs yet</p>
					</div>
				{:else}
					<div class="border border-kai-border rounded-md overflow-hidden">
						<table class="w-full">
							<thead class="bg-kai-bg-secondary">
								<tr class="text-left text-sm text-kai-text-muted">
									<th class="px-4 py-3 font-medium">Name</th>
									<th class="px-4 py-3 font-medium">Target</th>
									<th class="px-4 py-3 font-medium">Actor</th>
									<th class="px-4 py-3 font-medium">Updated</th>
								</tr>
							</thead>
							<tbody>
								{#each snapshots as ref}
									<tr class="border-t border-kai-border hover:bg-kai-bg-secondary">
										<td class="px-4 py-3">
											<span class="font-mono text-kai-accent">{ref.name}</span>
										</td>
										<td class="px-4 py-3">
											<code class="text-xs bg-kai-bg px-1.5 py-0.5 rounded font-mono">{shortHash(ref.target)}</code>
										</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{ref.actor || '-'}</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{formatDate(ref.updatedAt)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{:else if activeTab === 'changesets'}
				{#if changesets.length === 0}
					<div class="text-center py-8 text-kai-text-muted">
						<p>No changeset refs yet</p>
					</div>
				{:else}
					<div class="border border-kai-border rounded-md overflow-hidden">
						<table class="w-full">
							<thead class="bg-kai-bg-secondary">
								<tr class="text-left text-sm text-kai-text-muted">
									<th class="px-4 py-3 font-medium">Name</th>
									<th class="px-4 py-3 font-medium">Target</th>
									<th class="px-4 py-3 font-medium">Actor</th>
									<th class="px-4 py-3 font-medium">Updated</th>
								</tr>
							</thead>
							<tbody>
								{#each changesets as ref}
									<tr class="border-t border-kai-border hover:bg-kai-bg-secondary">
										<td class="px-4 py-3">
											<span class="font-mono text-kai-purple">{ref.name}</span>
										</td>
										<td class="px-4 py-3">
											<code class="text-xs bg-kai-bg px-1.5 py-0.5 rounded font-mono">{shortHash(ref.target)}</code>
										</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{ref.actor || '-'}</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{formatDate(ref.updatedAt)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{:else if activeTab === 'setup'}
				<div class="border border-kai-border rounded-md p-4">
					<h4 class="font-medium mb-3">Clone URL</h4>
					<div class="flex gap-2 items-center mb-6">
						<input type="text" readonly value={getCloneUrl()} class="input flex-1 font-mono text-sm bg-kai-bg" />
						<button
							class="btn"
							onclick={() => {
								navigator.clipboard.writeText(getCloneUrl());
							}}
						>
							Copy
						</button>
					</div>

					<h4 class="font-medium mb-3">Push to this repository</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">kai remote set origin {getCloneUrl()}
kai push origin snap.latest</pre>
					</div>
				</div>
			{/if}
		{/if}
	{/if}
</div>
