<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api, loadUser } from '$lib/api.js';
	import { marked } from 'marked';

	marked.setOptions({ gfm: true, breaks: true });

	function sanitizeHtml(html) {
		if (!html) return '';
		return html
			.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
			.replace(/<iframe\b[^<]*(?:(?!<\/iframe>)<[^<]*)*<\/iframe>/gi, '')
			.replace(/\bon\w+\s*=/gi, 'data-removed=')
			.replace(/javascript:/gi, 'removed:');
	}

	function safeMarkdown(content) {
		return sanitizeHtml(marked(content));
	}

	let run = $state(null);
	let jobs = $state([]);
	let selectedJob = $state(null);
	let logs = $state([]);
	let loading = $state(true);
	let logsLoading = $state(false);
	let error = $state('');
	let pollInterval = $state(null);
	let lastLogSeq = $state(-1);

	$effect(() => {
		// Re-run when page params change
		$page.params.slug;
		$page.params.repo;
		$page.params.id;
	});

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}
		await loadRun();
		startPolling();

		return () => {
			if (pollInterval) {
				clearInterval(pollInterval);
			}
		};
	});

	function startPolling() {
		pollInterval = setInterval(async () => {
			if (run && (run.status === 'queued' || run.status === 'in_progress')) {
				await loadRun(true);
				if (selectedJob) {
					await loadLogs(true);
				}
			}
		}, 3000);
	}

	async function loadRun(silent = false) {
		if (!silent) {
			loading = true;
			error = '';
		}
		const { slug, repo, id } = $page.params;

		try {
			const [runData, jobsData] = await Promise.all([
				api('GET', `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}`),
				api('GET', `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}/jobs`)
			]);

			if (runData.error) {
				error = runData.error;
				return;
			}

			run = runData;
			jobs = jobsData.jobs || [];

			// Auto-select first job if none selected
			if (!selectedJob && jobs.length > 0) {
				selectJob(jobs[0]);
			}
		} catch (e) {
			if (!silent) {
				error = 'Failed to load workflow run';
			}
		}

		if (!silent) {
			loading = false;
		}
	}

	async function selectJob(job) {
		selectedJob = job;
		lastLogSeq = -1;
		logs = [];
		await loadLogs();
	}

	async function loadLogs(append = false) {
		if (!selectedJob) return;

		logsLoading = !append;
		const { slug, repo, id } = $page.params;

		try {
			const url = append && lastLogSeq >= 0
				? `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}/jobs/${selectedJob.id}/logs?after=${lastLogSeq}`
				: `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}/jobs/${selectedJob.id}/logs`;

			const data = await api('GET', url);
			if (data.logs && data.logs.length > 0) {
				if (append) {
					logs = [...logs, ...data.logs];
				} else {
					logs = data.logs;
				}
				lastLogSeq = data.logs[data.logs.length - 1].chunk_seq;
			}
		} catch (e) {
			// Ignore log loading errors
		}

		logsLoading = false;
	}

	async function cancelRun() {
		const { slug, repo, id } = $page.params;
		try {
			await api('POST', `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}/cancel`);
			await loadRun();
		} catch (e) {
			error = 'Failed to cancel run';
		}
	}

	async function rerunWorkflow() {
		const { slug, repo, id } = $page.params;
		try {
			const data = await api('POST', `/api/v1/orgs/${slug}/repos/${repo}/runs/${id}/rerun`);
			if (data.id) {
				goto(`/orgs/${slug}/${repo}/workflows/runs/${data.id}`);
			}
		} catch (e) {
			error = 'Failed to re-run workflow';
		}
	}

	function getStatusColor(status, conclusion) {
		if (status === 'completed') {
			switch (conclusion) {
				case 'success':
					return 'bg-green-500/20 text-green-400 border-green-500/30';
				case 'failure':
					return 'bg-red-500/20 text-red-400 border-red-500/30';
				case 'cancelled':
					return 'bg-gray-500/20 text-gray-400 border-gray-500/30';
				default:
					return 'bg-gray-500/20 text-gray-400 border-gray-500/30';
			}
		}
		switch (status) {
			case 'queued':
				return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
			case 'in_progress':
				return 'bg-blue-500/20 text-blue-400 border-blue-500/30';
			case 'pending':
				return 'bg-gray-500/20 text-gray-400 border-gray-500/30';
			default:
				return 'bg-gray-500/20 text-gray-400 border-gray-500/30';
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
			case 'pending':
				return '○';
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

	function formatDuration(startedAt, completedAt) {
		if (!startedAt) return '-';
		const start = new Date(startedAt);
		const end = completedAt ? new Date(completedAt) : new Date();
		const diff = Math.floor((end - start) / 1000);

		if (diff < 60) return `${diff}s`;
		if (diff < 3600) return `${Math.floor(diff / 60)}m ${diff % 60}s`;
		return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`;
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleString('en-US', {
			month: 'short',
			day: 'numeric',
			year: 'numeric',
			hour: '2-digit',
			minute: '2-digit'
		});
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
</script>

<div class="max-w-7xl mx-auto px-5 py-8">
	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-400 mb-4">{error}</p>
			<button class="btn" onclick={() => loadRun()}>Retry</button>
		</div>
	{:else if run}
		<!-- Header -->
		<div class="mb-6">
			<nav class="text-sm text-kai-text-muted mb-2">
				<a href="/orgs/{$page.params.slug}" class="hover:text-kai-text">{$page.params.slug}</a>
				<span class="mx-2">/</span>
				<a href="/orgs/{$page.params.slug}/{$page.params.repo}" class="hover:text-kai-text"
					>{$page.params.repo}</a
				>
				<span class="mx-2">/</span>
				<a href="/orgs/{$page.params.slug}/{$page.params.repo}/workflows" class="hover:text-kai-text">Workflows</a>
				<span class="mx-2">/</span>
				<span>Run #{run.run_number}</span>
			</nav>

			<div class="flex items-start justify-between">
				<div>
					<div class="flex items-center gap-3">
						<h2 class="text-xl font-semibold">{run.workflow_name || 'Workflow'}</h2>
						<span class="px-2 py-1 rounded text-sm font-medium {getStatusColor(run.status, run.conclusion)}">
							{getStatusIcon(run.status, run.conclusion)} {formatStatus(run.status, run.conclusion)}
						</span>
					</div>
					<div class="text-kai-text-muted text-sm mt-2 flex items-center gap-4">
						<span>Triggered by <strong>{run.trigger_event}</strong></span>
						{#if run.trigger_ref}
							<span>on <strong>{getRefDisplay(run.trigger_ref)}</strong></span>
						{/if}
						{#if run.trigger_sha}
							<span class="font-mono bg-kai-bg-tertiary px-2 py-0.5 rounded">{run.trigger_sha.slice(0, 7)}</span>
						{/if}
						{#if run.created_at}
							<span>{formatDate(run.created_at)}</span>
						{/if}
					</div>
				</div>
				<div class="flex gap-2">
					{#if run.status === 'queued' || run.status === 'in_progress'}
						<button class="btn btn-danger btn-sm" onclick={cancelRun}>Cancel</button>
					{:else}
						<button class="btn btn-secondary btn-sm" onclick={rerunWorkflow}>Re-run</button>
					{/if}
				</div>
			</div>
		</div>

		<!-- Jobs and Logs -->
		<div class="grid grid-cols-12 gap-6">
			<!-- Jobs Sidebar -->
			<div class="col-span-3">
				<div class="card p-0">
					<div class="p-3 border-b border-kai-border">
						<h3 class="font-medium">Jobs</h3>
					</div>
					{#each jobs as job}
						<button
							class="w-full text-left px-3 py-2 border-b border-kai-border last:border-b-0 hover:bg-kai-bg-tertiary transition-colors {selectedJob?.id === job.id ? 'bg-kai-bg-tertiary' : ''}"
							onclick={() => selectJob(job)}
						>
							<div class="flex items-center gap-2">
								<span class="w-5 h-5 rounded-full flex items-center justify-center text-xs font-bold border {getStatusColor(job.status, job.conclusion)}">
									{getStatusIcon(job.status, job.conclusion)}
								</span>
								<span class="truncate flex-1 text-sm">{job.name}</span>
								<span class="text-kai-text-muted text-xs">{formatDuration(job.started_at, job.completed_at)}</span>
							</div>
							{#if job.steps && job.steps.length > 0}
								<div class="ml-7 mt-1 space-y-0.5">
									{#each job.steps as step}
										<div class="flex items-center gap-2 text-xs text-kai-text-muted">
											<span class="w-4 h-4 rounded flex items-center justify-center text-[10px] {getStatusColor(step.status, step.conclusion)}">
												{getStatusIcon(step.status, step.conclusion)}
											</span>
											<span class="truncate">{step.name}</span>
										</div>
									{/each}
								</div>
							{/if}
						</button>
					{/each}
				</div>
			</div>

			<!-- Logs Panel -->
			<div class="col-span-9 space-y-4">
				{#if selectedJob?.summary}
					<div class="card p-0">
						<div class="p-3 border-b border-kai-border">
							<h3 class="font-medium">Summary</h3>
						</div>
						<div class="p-4 prose prose-invert max-w-none text-sm">
							{@html safeMarkdown(selectedJob.summary)}
						</div>
					</div>
				{/if}
				<div class="card p-0 h-[600px] flex flex-col">
					<div class="p-3 border-b border-kai-border flex items-center justify-between">
						<h3 class="font-medium">
							{selectedJob ? selectedJob.name : 'Select a job'}
						</h3>
						{#if selectedJob}
							<span class="text-kai-text-muted text-sm">
								{formatDuration(selectedJob.started_at, selectedJob.completed_at)}
							</span>
						{/if}
					</div>
					<div class="flex-1 overflow-auto bg-kai-bg p-4 font-mono text-sm">
						{#if logsLoading}
							<div class="text-kai-text-muted">Loading logs...</div>
						{:else if logs.length === 0}
							<div class="text-kai-text-muted">
								{#if selectedJob}
									{#if selectedJob.status === 'queued' || selectedJob.status === 'pending'}
										Waiting for job to start...
									{:else}
										No logs available
									{/if}
								{:else}
									Select a job to view logs
								{/if}
							</div>
						{:else}
							{#each logs as log}
								<div class="whitespace-pre-wrap text-kai-text-muted hover:text-kai-text leading-relaxed">{log.content}</div>
							{/each}
						{/if}
					</div>
				</div>
			</div>
		</div>
	{/if}
</div>

<style>
	/* Additional styles for log display */
	.font-mono {
		font-family: 'SF Mono', 'Monaco', 'Inconsolata', 'Fira Code', monospace;
	}
</style>
