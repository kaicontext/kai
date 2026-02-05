<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api, loadUser } from '$lib/api.js';

	let runs = $state([]);
	let loading = $state(true);
	let error = $state('');
	let pollInterval = $state(null);

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
		await loadRuns();

		// Poll for updates when there are in-progress runs
		startPolling();

		return () => {
			if (pollInterval) {
				clearInterval(pollInterval);
			}
		};
	});

	function startPolling() {
		pollInterval = setInterval(async () => {
			const hasInProgress = runs.some(r => r.status === 'queued' || r.status === 'in_progress');
			if (hasInProgress) {
				await loadRuns(true);
			}
		}, 5000);
	}

	async function loadRuns(silent = false) {
		if (!silent) {
			loading = true;
			error = '';
		}
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}/runs?limit=50`);
			if (data.error) {
				error = data.error;
				runs = [];
			} else {
				runs = data.runs || [];
			}
		} catch (e) {
			if (!silent) {
				error = 'Failed to load workflow runs';
			}
			runs = [];
		}

		if (!silent) {
			loading = false;
		}
	}

	function getStatusColor(status, conclusion) {
		if (status === 'completed') {
			switch (conclusion) {
				case 'success':
					return 'bg-green-500/20 text-green-400';
				case 'failure':
					return 'bg-red-500/20 text-red-400';
				case 'cancelled':
					return 'bg-gray-500/20 text-gray-400';
				default:
					return 'bg-gray-500/20 text-gray-400';
			}
		}
		switch (status) {
			case 'queued':
				return 'bg-yellow-500/20 text-yellow-400';
			case 'in_progress':
				return 'bg-blue-500/20 text-blue-400';
			default:
				return 'bg-gray-500/20 text-gray-400';
		}
	}

	function getStatusIcon(status, conclusion) {
		if (status === 'completed') {
			switch (conclusion) {
				case 'success':
					return '✓';
				case 'failure':
					return '✕';
				case 'cancelled':
					return '⊘';
				default:
					return '?';
			}
		}
		switch (status) {
			case 'queued':
				return '◦';
			case 'in_progress':
				return '●';
			default:
				return '?';
		}
	}

	function formatStatus(status, conclusion) {
		if (status === 'completed') {
			return conclusion.charAt(0).toUpperCase() + conclusion.slice(1);
		}
		return status.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleDateString('en-US', {
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
	}

	function formatDuration(startedAt, completedAt) {
		if (!startedAt) return '';
		const start = new Date(startedAt);
		const end = completedAt ? new Date(completedAt) : new Date();
		const diff = Math.floor((end - start) / 1000);

		if (diff < 60) return `${diff}s`;
		if (diff < 3600) return `${Math.floor(diff / 60)}m ${diff % 60}s`;
		return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`;
	}

	function getRefDisplay(ref) {
		if (!ref) return '';
		if (ref.startsWith('refs/heads/')) {
			return ref.replace('refs/heads/', '');
		}
		if (ref.startsWith('refs/tags/')) {
			return ref.replace('refs/tags/', '');
		}
		return ref;
	}

	function viewRun(run) {
		const { slug, repo } = $page.params;
		goto(`/orgs/${slug}/${repo}/workflows/runs/${run.id}`);
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
				<span>Workflows</span>
			</nav>
			<h2 class="text-xl font-semibold">Workflow Runs</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				CI/CD workflow executions
			</p>
		</div>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-400 mb-4">{error}</p>
			<button class="btn" onclick={() => loadRuns()}>Retry</button>
		</div>
	{:else if runs.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🚀</div>
			<p class="text-kai-text-muted mb-4">No workflow runs yet</p>
			<p class="text-kai-text-muted text-sm">
				Create workflows in <code class="bg-kai-bg-tertiary px-2 py-1 rounded">.kailab/workflows/</code> to get started
			</p>
		</div>
	{:else}
		<div class="card p-0">
			{#each runs as run}
				<button
					class="list-item w-full text-left hover:bg-kai-bg-tertiary transition-colors cursor-pointer"
					onclick={() => viewRun(run)}
				>
					<div class="flex items-center gap-3 min-w-0 flex-1">
						<span class="w-6 h-6 rounded-full flex items-center justify-center text-sm font-bold {getStatusColor(run.status, run.conclusion)}">
							{getStatusIcon(run.status, run.conclusion)}
						</span>
						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2">
								<span class="font-medium truncate">{run.workflow_name || 'Workflow'}</span>
								<span class="text-kai-text-muted text-xs">#{run.run_number}</span>
							</div>
							<div class="text-kai-text-muted text-xs mt-1 flex items-center gap-3">
								<span class="px-1.5 py-0.5 rounded bg-kai-bg-tertiary">{run.trigger_event}</span>
								{#if run.trigger_ref}
									<span class="truncate">{getRefDisplay(run.trigger_ref)}</span>
								{/if}
								{#if run.trigger_sha}
									<span class="font-mono">{run.trigger_sha.slice(0, 7)}</span>
								{/if}
							</div>
						</div>
					</div>
					<div class="text-right text-sm">
						<div class="px-2 py-0.5 rounded text-xs font-medium {getStatusColor(run.status, run.conclusion)}">
							{formatStatus(run.status, run.conclusion)}
						</div>
						<div class="text-kai-text-muted text-xs mt-1">
							{#if run.started_at}
								{formatDuration(run.started_at, run.completed_at)}
							{/if}
							{#if run.created_at}
								<span class="ml-2">{formatDate(run.created_at)}</span>
							{/if}
						</div>
					</div>
				</button>
			{/each}
		</div>
	{/if}
</div>
