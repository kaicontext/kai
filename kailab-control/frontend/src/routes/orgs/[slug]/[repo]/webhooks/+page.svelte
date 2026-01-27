<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let webhooks = $state([]);
	let loading = $state(true);
	let showCreateModal = $state(false);
	let showDeliveriesModal = $state(false);
	let selectedWebhook = $state(null);
	let deliveries = $state([]);
	let loadingDeliveries = $state(false);

	// Form state
	let newUrl = $state('');
	let newSecret = $state('');
	let newEvents = $state(['push']);
	let error = $state('');

	const allEvents = [
		{ value: 'push', label: 'Push', description: 'Any push to a branch' },
		{ value: 'branch_create', label: 'Branch created', description: 'A new branch is created' },
		{ value: 'branch_delete', label: 'Branch deleted', description: 'A branch is deleted' },
		{ value: 'tag_create', label: 'Tag created', description: 'A new tag is created' },
		{ value: 'tag_delete', label: 'Tag deleted', description: 'A tag is deleted' }
	];

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
		await loadWebhooks();
	});

	async function loadWebhooks() {
		loading = true;
		const { slug, repo } = $page.params;
		const data = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}/webhooks`);
		webhooks = data.webhooks || [];
		loading = false;
	}

	async function createWebhook() {
		error = '';

		if (!newUrl.trim()) {
			error = 'URL is required';
			return;
		}
		if (!newUrl.startsWith('http://') && !newUrl.startsWith('https://')) {
			error = 'URL must start with http:// or https://';
			return;
		}
		if (newEvents.length === 0) {
			error = 'Select at least one event';
			return;
		}

		const { slug, repo } = $page.params;
		const data = await api('POST', `/api/v1/orgs/${slug}/repos/${repo}/webhooks`, {
			url: newUrl.trim(),
			secret: newSecret.trim() || undefined,
			events: newEvents
		});

		if (data.error) {
			error = data.error;
			return;
		}

		showCreateModal = false;
		resetForm();
		await loadWebhooks();
	}

	async function toggleWebhook(webhook) {
		const { slug, repo } = $page.params;
		await api('PATCH', `/api/v1/orgs/${slug}/repos/${repo}/webhooks/${webhook.id}`, {
			active: !webhook.active
		});
		await loadWebhooks();
	}

	async function deleteWebhook(webhook) {
		if (!confirm(`Delete webhook to ${webhook.url}?`)) return;

		const { slug, repo } = $page.params;
		await api('DELETE', `/api/v1/orgs/${slug}/repos/${repo}/webhooks/${webhook.id}`);
		await loadWebhooks();
	}

	async function viewDeliveries(webhook) {
		selectedWebhook = webhook;
		loadingDeliveries = true;
		showDeliveriesModal = true;

		const { slug, repo } = $page.params;
		const data = await api(
			'GET',
			`/api/v1/orgs/${slug}/repos/${repo}/webhooks/${webhook.id}/deliveries`
		);
		deliveries = data.deliveries || [];
		loadingDeliveries = false;
	}

	function resetForm() {
		newUrl = '';
		newSecret = '';
		newEvents = ['push'];
		error = '';
	}

	function closeCreateModal() {
		showCreateModal = false;
		resetForm();
	}

	function closeDeliveriesModal() {
		showDeliveriesModal = false;
		selectedWebhook = null;
		deliveries = [];
	}

	function toggleEvent(event) {
		if (newEvents.includes(event)) {
			newEvents = newEvents.filter((e) => e !== event);
		} else {
			newEvents = [...newEvents, event];
		}
	}

	function formatDate(dateStr) {
		if (!dateStr) return 'Never';
		return new Date(dateStr).toLocaleString();
	}

	function truncateUrl(url) {
		if (url.length > 50) {
			return url.slice(0, 47) + '...';
		}
		return url;
	}

	function getStatusColor(status) {
		switch (status) {
			case 'success':
				return 'text-green-400';
			case 'failed':
				return 'text-red-400';
			default:
				return 'text-yellow-400';
		}
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
				<span>Webhooks</span>
			</nav>
			<h2 class="text-xl font-semibold">Webhooks</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				Get notified when events happen in this repository
			</p>
		</div>
		<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>Add Webhook</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if webhooks.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🔔</div>
			<p class="text-kai-text-muted mb-4">No webhooks configured</p>
			<p class="text-kai-text-muted text-sm mb-6">
				Webhooks let you trigger external services when events happen in this repo.
			</p>
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
				Add your first webhook
			</button>
		</div>
	{:else}
		<div class="card p-0">
			{#each webhooks as webhook}
				<div class="list-item">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2">
							<span
								class="w-2 h-2 rounded-full {webhook.active ? 'bg-green-400' : 'bg-gray-500'}"
							></span>
							<span class="font-medium font-mono text-sm">{truncateUrl(webhook.url)}</span>
						</div>
						<div class="text-kai-text-muted text-xs mt-1">
							Events: {webhook.events.join(', ')}
						</div>
					</div>
					<div class="flex items-center gap-2">
						<button
							class="btn btn-sm"
							onclick={() => viewDeliveries(webhook)}
							title="View deliveries"
						>
							History
						</button>
						<button
							class="btn btn-sm"
							onclick={() => toggleWebhook(webhook)}
							title={webhook.active ? 'Disable' : 'Enable'}
						>
							{webhook.active ? 'Disable' : 'Enable'}
						</button>
						<button class="btn btn-sm btn-danger" onclick={() => deleteWebhook(webhook)}>
							Delete
						</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<div class="mt-8 card">
		<h3 class="font-semibold mb-3">Webhook Payload</h3>
		<p class="text-kai-text-muted text-sm mb-4">Webhooks are sent as POST requests with JSON body:</p>
		<div class="code-block text-xs">
			<pre>{`{
  "event": "push",
  "repository": {
    "id": "...",
    "name": "repo",
    "full_name": "org/repo"
  },
  "refs": ["snap.main"]
}`}</pre>
		</div>
		<p class="text-kai-text-muted text-sm mt-4">
			If you provide a secret, we'll include a <code class="text-xs bg-kai-bg-tertiary px-1 py-0.5 rounded">X-Kailab-Signature-256</code> header with an HMAC-SHA256 signature.
		</p>
	</div>
</div>

{#if showCreateModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={closeCreateModal}
		onkeydown={(e) => e.key === 'Escape' && closeCreateModal()}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-lg w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
			tabindex="0"
		>
			<h3 class="text-lg font-semibold mb-4">Add Webhook</h3>

			{#if error}
				<div class="alert alert-error mb-4">{error}</div>
			{/if}

			<div class="mb-4">
				<label for="webhook-url" class="block mb-2 font-medium">Payload URL</label>
				<input
					type="url"
					id="webhook-url"
					bind:value={newUrl}
					class="input"
					placeholder="https://example.com/webhook"
				/>
			</div>

			<div class="mb-4">
				<label for="webhook-secret" class="block mb-2 font-medium">Secret (optional)</label>
				<input
					type="text"
					id="webhook-secret"
					bind:value={newSecret}
					class="input font-mono"
					placeholder="your-secret-token"
				/>
				<p class="text-kai-text-muted text-xs mt-1">
					Used to sign payloads for verification
				</p>
			</div>

			<div class="mb-4">
				<label class="block mb-2 font-medium">Events</label>
				<div class="space-y-2">
					{#each allEvents as event}
						<label class="flex items-start gap-3 cursor-pointer">
							<input
								type="checkbox"
								checked={newEvents.includes(event.value)}
								onchange={() => toggleEvent(event.value)}
								class="mt-1"
							/>
							<div>
								<div class="text-sm">{event.label}</div>
								<div class="text-xs text-kai-text-muted">{event.description}</div>
							</div>
						</label>
					{/each}
				</div>
			</div>

			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={closeCreateModal}>Cancel</button>
				<button class="btn btn-primary" onclick={createWebhook}>Add Webhook</button>
			</div>
		</div>
	</div>
{/if}

{#if showDeliveriesModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={closeDeliveriesModal}
		onkeydown={(e) => e.key === 'Escape' && closeDeliveriesModal()}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-2xl w-11/12 max-h-[80vh] overflow-hidden flex flex-col"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
			tabindex="0"
		>
			<h3 class="text-lg font-semibold mb-4">
				Delivery History
				{#if selectedWebhook}
					<span class="text-sm font-normal text-kai-text-muted ml-2">
						{truncateUrl(selectedWebhook.url)}
					</span>
				{/if}
			</h3>

			<div class="flex-1 overflow-auto">
				{#if loadingDeliveries}
					<div class="text-center py-8 text-kai-text-muted">Loading...</div>
				{:else if deliveries.length === 0}
					<div class="text-center py-8 text-kai-text-muted">No deliveries yet</div>
				{:else}
					<div class="space-y-2">
						{#each deliveries as delivery}
							<div class="bg-kai-bg p-3 rounded-lg">
								<div class="flex items-center justify-between">
									<div class="flex items-center gap-2">
										<span class={getStatusColor(delivery.status)}>
											{delivery.status === 'success' ? '✓' : delivery.status === 'failed' ? '✗' : '○'}
										</span>
										<span class="font-mono text-sm">{delivery.event}</span>
									</div>
									<div class="text-xs text-kai-text-muted">
										{formatDate(delivery.created_at)}
									</div>
								</div>
								{#if delivery.response_code}
									<div class="text-xs text-kai-text-muted mt-1">
										Response: {delivery.response_code}
										{#if delivery.attempts > 1}
											<span class="ml-2">({delivery.attempts} attempts)</span>
										{/if}
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<div class="flex justify-end mt-4 pt-4 border-t border-kai-border">
				<button class="btn" onclick={closeDeliveriesModal}>Close</button>
			</div>
		</div>
	</div>
{/if}
