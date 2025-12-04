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
	let compareBase = $state('');
	let compareHead = $state('');
	let diffResult = $state(null);
	let diffLoading = $state(false);

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

	// Compare refs for diff
	async function compareDiff() {
		if (!compareBase || !compareHead) return;
		diffLoading = true;
		diffResult = null;

		// In the future, this could call a server-side diff API
		// For now, show a placeholder that explains CLI usage
		await new Promise(resolve => setTimeout(resolve, 500));

		diffResult = {
			base: compareBase,
			head: compareHead,
			message: 'Semantic diff is available via CLI',
			cliCommand: `kai diff @snap:${compareBase} @snap:${compareHead} --semantic`
		};
		diffLoading = false;
	}

	function getActionClass(action) {
		switch(action) {
			case 'added': return 'text-green-400';
			case 'removed': return 'text-red-400';
			case 'modified': return 'text-yellow-400';
			default: return '';
		}
	}

	function getActionIcon(action) {
		switch(action) {
			case 'added': return '+';
			case 'removed': return '-';
			case 'modified': return '~';
			default: return ' ';
		}
	}
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
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'compare' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => activeTab = 'compare'}
					>
						Compare
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
			{:else if activeTab === 'compare'}
				<div class="border border-kai-border rounded-md p-4">
					<h4 class="font-medium mb-4">Compare Snapshots</h4>

					{#if snapshots.length < 2}
						<div class="text-center py-8 text-kai-text-muted">
							<p>Need at least 2 snapshots to compare</p>
						</div>
					{:else}
						<div class="grid grid-cols-2 gap-4 mb-4">
							<div>
								<label class="block text-sm text-kai-text-muted mb-1">Base (older)</label>
								<select bind:value={compareBase} class="input w-full font-mono">
									<option value="">Select snapshot...</option>
									{#each snapshots as ref}
										<option value={ref.name.replace('snap.', '')}>{ref.name}</option>
									{/each}
								</select>
							</div>
							<div>
								<label class="block text-sm text-kai-text-muted mb-1">Head (newer)</label>
								<select bind:value={compareHead} class="input w-full font-mono">
									<option value="">Select snapshot...</option>
									{#each snapshots as ref}
										<option value={ref.name.replace('snap.', '')}>{ref.name}</option>
									{/each}
								</select>
							</div>
						</div>

						<button
							class="btn btn-primary"
							disabled={!compareBase || !compareHead || compareBase === compareHead || diffLoading}
							onclick={compareDiff}
						>
							{diffLoading ? 'Comparing...' : 'Compare'}
						</button>

						{#if diffResult}
							<div class="mt-6 border-t border-kai-border pt-4">
								<div class="mb-4">
									<span class="font-mono text-sm">
										{diffResult.base} → {diffResult.head}
									</span>
								</div>

								<div class="bg-kai-bg rounded p-4">
									<p class="text-kai-text-muted mb-3">{diffResult.message}</p>
									<div class="bg-kai-bg-secondary rounded p-3">
										<code class="text-sm font-mono text-kai-accent">{diffResult.cliCommand}</code>
									</div>
									<button
										class="btn mt-3"
										onclick={() => navigator.clipboard.writeText(diffResult.cliCommand)}
									>
										Copy Command
									</button>
								</div>

								<div class="mt-4 text-sm text-kai-text-muted">
									<p class="mb-2">
										<strong>Tip:</strong> The <code class="bg-kai-bg px-1 rounded">kai diff --semantic</code> command shows:
									</p>
									<ul class="list-disc list-inside space-y-1">
										<li>Functions added, removed, or modified</li>
										<li>Classes and methods changed</li>
										<li>JSON/YAML key changes</li>
										<li>SQL table and column modifications</li>
									</ul>
								</div>
							</div>
						{/if}
					{/if}
				</div>
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
