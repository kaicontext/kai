<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser, currentOrg, currentRepo } from '$lib/stores.js';
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
	let showDeleteConfirm = $state(false);
	let deleting = $state(false);

	// Files tab state
	let selectedSnapshot = $state('');
	let files = $state([]);
	let filesLoading = $state(false);
	let selectedFile = $state(null);
	let fileContent = $state('');
	let fileContentLoading = $state(false);

	// Reviews tab state
	let reviews = $state([]);
	let reviewsLoading = $state(false);

	$effect(() => {
		currentOrg.set($page.params.slug);
		currentRepo.set($page.params.repo);
	});

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
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

	async function loadReviews() {
		reviewsLoading = true;
		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/reviews`);
		if (data.reviews) {
			reviews = data.reviews;
		}
		reviewsLoading = false;
	}

	async function deleteRepo() {
		deleting = true;
		const result = await api('DELETE', `/api/v1/orgs/${$page.params.slug}/repos/${$page.params.repo}`);
		deleting = false;
		if (!result.error) {
			goto(`/orgs/${$page.params.slug}`);
		}
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
	// Only show main workspace refs (ws.<name>), not helper refs (ws.<name>.base, .head, .cs.*)
	let workspaces = $derived(refs.filter(r => {
		if (!r.name.startsWith('ws.')) return false;
		const wsName = r.name.slice(3); // Remove 'ws.' prefix
		// Main workspace refs don't have a dot in the name (e.g., 'feat/init')
		// Helper refs have dots (e.g., 'feat/init.base', 'feat/init.head', 'feat/init.cs.abc123')
		const isMain = !wsName.includes('.');
		console.log('workspace filter:', r.name, 'wsName:', wsName, 'isMain:', isMain);
		return isMain;
	}));
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

	// Files tab functions
	async function loadFiles(snapshotRef) {
		if (!snapshotRef) {
			files = [];
			return;
		}
		filesLoading = true;
		selectedFile = null;
		fileContent = '';

		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/files/${snapshotRef}`);
		if (data.files) {
			files = data.files.sort((a, b) => a.path.localeCompare(b.path));
		} else {
			files = [];
		}
		filesLoading = false;
	}

	async function loadFileContent(file) {
		selectedFile = file;
		fileContentLoading = true;
		fileContent = '';

		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/content/${file.digest}`);
		if (data.content) {
			// Content is base64 encoded
			try {
				fileContent = atob(data.content);
			} catch {
				fileContent = '(Binary file)';
			}
		}
		fileContentLoading = false;
	}

	// Build file tree structure
	function buildFileTree(fileList) {
		const tree = {};
		for (const file of fileList) {
			const parts = file.path.split('/');
			let current = tree;
			for (let i = 0; i < parts.length - 1; i++) {
				const part = parts[i];
				if (!current[part]) {
					current[part] = { _isDir: true, _children: {} };
				}
				current = current[part]._children;
			}
			const fileName = parts[parts.length - 1];
			current[fileName] = { _isDir: false, _file: file };
		}
		return tree;
	}

	function getLangIcon(lang) {
		const icons = {
			'ts': '🔷',
			'tsx': '🔷',
			'js': '🟨',
			'jsx': '🟨',
			'py': '🐍',
			'go': '🔵',
			'json': '📋',
			'yaml': '📋',
			'yml': '📋',
			'sql': '🗃️',
			'md': '📝',
			'css': '🎨',
			'html': '🌐'
		};
		return icons[lang] || '📄';
	}

	let fileTree = $derived(buildFileTree(files));
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
			<button
				class="btn btn-danger text-sm"
				onclick={() => showDeleteConfirm = true}
			>
				Delete
			</button>
		</div>

		<!-- Delete Confirmation Modal -->
		{#if showDeleteConfirm}
			<div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
				<div class="bg-kai-bg-secondary border border-kai-border rounded-lg p-6 max-w-md w-full mx-4">
					<h3 class="text-lg font-semibold mb-2">Delete Repository</h3>
					<p class="text-kai-text-muted mb-4">
						Are you sure you want to delete <strong>{$page.params.slug}/{$page.params.repo}</strong>?
						This action cannot be undone.
					</p>
					<div class="flex gap-3 justify-end">
						<button
							class="btn"
							onclick={() => showDeleteConfirm = false}
							disabled={deleting}
						>
							Cancel
						</button>
						<button
							class="btn btn-danger"
							onclick={deleteRepo}
							disabled={deleting}
						>
							{deleting ? 'Deleting...' : 'Delete'}
						</button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Empty state - GitHub/GitLab style setup instructions -->
		{#if refs.length === 0}
			<div class="border border-kai-border rounded-md">
				<!-- Quick setup header -->
				<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border">
					<h3 class="font-semibold">Quick setup</h3>
				</div>

				<!-- Install kai-cli -->
				<div class="p-4 border-b border-kai-border">
					<h4 class="font-medium mb-3">1. Install the Kai CLI</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">curl -fsSL https://kaiscm.com/install.sh | sh</pre>
					</div>
					<p class="text-kai-text-muted text-xs mt-2">
						Or with Go: <code class="bg-kai-bg-tertiary px-1 rounded">go install gitlab.com/preplan/kai/kai-cli/cmd/kai@latest</code>
					</p>
				</div>

				<div class="p-4 border-b border-kai-border">
					<h4 class="font-medium mb-3">2. Remote URL</h4>
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
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'reviews' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => { activeTab = 'reviews'; if (reviews.length === 0 && !reviewsLoading) loadReviews(); }}
					>
						Reviews
						{#if reviews.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{reviews.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'files' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => activeTab = 'files'}
					>
						Files
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
			{:else if activeTab === 'reviews'}
				{#if reviewsLoading}
					<div class="text-center py-8 text-kai-text-muted">
						<p>Loading reviews...</p>
					</div>
				{:else if reviews.length === 0}
					<div class="text-center py-8 text-kai-text-muted">
						<p>No reviews yet</p>
						<p class="text-sm mt-2">Create a review with: <code class="bg-kai-bg px-1.5 py-0.5 rounded">kai review open @cs:last</code></p>
					</div>
				{:else}
					<div class="border border-kai-border rounded-md overflow-hidden">
						<table class="w-full">
							<thead class="bg-kai-bg-secondary">
								<tr class="text-left text-sm text-kai-text-muted">
									<th class="px-4 py-3 font-medium">Title</th>
									<th class="px-4 py-3 font-medium">State</th>
									<th class="px-4 py-3 font-medium">Author</th>
									<th class="px-4 py-3 font-medium">Target</th>
									<th class="px-4 py-3 font-medium">Updated</th>
								</tr>
							</thead>
							<tbody>
								{#each reviews as review}
									<tr class="border-t border-kai-border hover:bg-kai-bg-secondary">
										<td class="px-4 py-3">
											<span class="font-medium">{review.title || '(untitled)'}</span>
											<span class="ml-2 text-xs text-kai-text-muted font-mono">{review.id}</span>
										</td>
										<td class="px-4 py-3">
											<span class="px-2 py-0.5 text-xs rounded-full {
												review.state === 'open' ? 'bg-green-500/20 text-green-400' :
												review.state === 'approved' ? 'bg-blue-500/20 text-blue-400' :
												review.state === 'changes_requested' ? 'bg-yellow-500/20 text-yellow-400' :
												review.state === 'merged' ? 'bg-purple-500/20 text-purple-400' :
												review.state === 'abandoned' ? 'bg-red-500/20 text-red-400' :
												'bg-kai-bg-tertiary text-kai-text-muted'
											}">{review.state}</span>
										</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{review.author || '-'}</td>
										<td class="px-4 py-3">
											<code class="text-xs bg-kai-bg px-1.5 py-0.5 rounded font-mono">{review.targetKind}:{review.targetId?.substring(0, 12)}</code>
										</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{formatDate(review.updatedAt)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{:else if activeTab === 'files'}
				<div class="border border-kai-border rounded-md">
					<!-- Snapshot selector -->
					<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border">
						<div class="flex items-center gap-4">
							<label class="text-sm text-kai-text-muted">Snapshot:</label>
							<select
								bind:value={selectedSnapshot}
								onchange={() => loadFiles(selectedSnapshot)}
								class="input w-64 font-mono text-sm"
							>
								<option value="">Select a snapshot...</option>
								{#each snapshots as ref}
									<option value={ref.name}>{ref.name}</option>
								{/each}
							</select>
							{#if filesLoading}
								<span class="text-kai-text-muted text-sm">Loading...</span>
							{:else if files.length > 0}
								<span class="text-kai-text-muted text-sm">{files.length} files</span>
							{/if}
						</div>
					</div>

					{#if !selectedSnapshot}
						<div class="text-center py-12 text-kai-text-muted">
							<p>Select a snapshot to browse files</p>
						</div>
					{:else if filesLoading}
						<div class="text-center py-12 text-kai-text-muted">
							<p>Loading files...</p>
						</div>
					{:else if files.length === 0}
						<div class="text-center py-12 text-kai-text-muted">
							<p>No files in this snapshot</p>
							<p class="text-xs mt-2">This snapshot may have been created before file tracking was enabled.</p>
						</div>
					{:else}
						<div class="flex" style="min-height: 400px;">
							<!-- File tree -->
							<div class="w-72 border-r border-kai-border overflow-auto" style="max-height: 600px;">
								<div class="p-2">
									{#each files as file}
										<button
											class="w-full text-left px-2 py-1 rounded text-sm font-mono hover:bg-kai-bg-tertiary transition-colors flex items-center gap-2 {selectedFile?.digest === file.digest ? 'bg-kai-bg-tertiary text-kai-accent' : 'text-kai-text'}"
											onclick={() => loadFileContent(file)}
										>
											<span class="text-xs">{getLangIcon(file.lang)}</span>
											<span class="truncate">{file.path}</span>
										</button>
									{/each}
								</div>
							</div>

							<!-- File content viewer -->
							<div class="flex-1 overflow-auto" style="max-height: 600px;">
								{#if !selectedFile}
									<div class="flex items-center justify-center h-full text-kai-text-muted">
										<p>Select a file to view</p>
									</div>
								{:else if fileContentLoading}
									<div class="flex items-center justify-center h-full text-kai-text-muted">
										<p>Loading...</p>
									</div>
								{:else}
									<div class="p-4">
										<div class="flex items-center justify-between mb-3 pb-2 border-b border-kai-border">
											<div class="flex items-center gap-2">
												<span>{getLangIcon(selectedFile.lang)}</span>
												<span class="font-mono text-sm">{selectedFile.path}</span>
											</div>
											<button
												class="btn text-xs"
												onclick={() => navigator.clipboard.writeText(fileContent)}
											>
												Copy
											</button>
										</div>
										<pre class="text-sm font-mono whitespace-pre-wrap break-all bg-kai-bg p-4 rounded border border-kai-border overflow-auto">{fileContent}</pre>
									</div>
								{/if}
							</div>
						</div>
					{/if}
				</div>
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
				<div class="border border-kai-border rounded-md">
					<div class="p-4 border-b border-kai-border">
						<h4 class="font-medium mb-3">Install the Kai CLI</h4>
						<div class="code-block bg-kai-bg">
							<pre class="text-sm">curl -fsSL https://kaiscm.com/install.sh | sh</pre>
						</div>
						<p class="text-kai-text-muted text-xs mt-2">
							Or with Go: <code class="bg-kai-bg-tertiary px-1 rounded">go install gitlab.com/preplan/kai/kai-cli/cmd/kai@latest</code>
						</p>
					</div>

					<div class="p-4 border-b border-kai-border">
						<h4 class="font-medium mb-3">Clone URL</h4>
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

					<div class="p-4 border-b border-kai-border">
						<h4 class="font-medium mb-3">Clone this repository</h4>
						<div class="code-block bg-kai-bg">
							<pre class="text-sm">kai clone {$page.params.slug}/{$page.params.repo}</pre>
						</div>
					</div>

					<div class="p-4">
						<h4 class="font-medium mb-3">Push to this repository</h4>
						<div class="code-block bg-kai-bg">
							<pre class="text-sm">kai remote set origin {getCloneUrl()}
kai push origin snap.latest</pre>
						</div>
					</div>
				</div>
			{/if}
		{/if}
	{/if}
</div>
