<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api, loadUser } from '$lib/api.js';

	let secrets = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showAddModal = $state(false);
	let newSecretName = $state('');
	let newSecretValue = $state('');
	let saving = $state(false);
	let deleteConfirm = $state(null);

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
		await loadSecrets();
	});

	async function loadSecrets() {
		loading = true;
		error = '';
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}/secrets`);
			if (data.error) {
				error = data.error;
				secrets = [];
			} else {
				secrets = data.secrets || [];
			}
		} catch (e) {
			error = 'Failed to load secrets';
			secrets = [];
		}

		loading = false;
	}

	async function addSecret() {
		if (!newSecretName.trim() || !newSecretValue.trim()) {
			return;
		}

		saving = true;
		const { slug, repo } = $page.params;

		try {
			const result = await api('PUT', `/api/v1/orgs/${slug}/repos/${repo}/secrets/${newSecretName}`, {
				value: newSecretValue
			});

			if (result.error) {
				error = result.error;
			} else {
				showAddModal = false;
				newSecretName = '';
				newSecretValue = '';
				await loadSecrets();
			}
		} catch (e) {
			error = 'Failed to save secret';
		}

		saving = false;
	}

	async function deleteSecret(name) {
		const { slug, repo } = $page.params;

		try {
			await api('DELETE', `/api/v1/orgs/${slug}/repos/${repo}/secrets/${name}`);
			deleteConfirm = null;
			await loadSecrets();
		} catch (e) {
			error = 'Failed to delete secret';
		}
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleDateString('en-US', {
			month: 'short',
			day: 'numeric',
			year: 'numeric'
		});
	}

	function isValidName(name) {
		return /^[A-Z_][A-Z0-9_]*$/i.test(name);
	}
</script>

<div class="max-w-4xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<div>
			<nav class="text-sm text-kai-text-muted mb-2">
				<a href="/orgs/{$page.params.slug}" class="hover:text-kai-text">{$page.params.slug}</a>
				<span class="mx-2">/</span>
				<a href="/orgs/{$page.params.slug}/{$page.params.repo}" class="hover:text-kai-text"
					>{$page.params.repo}</a
				>
				<span class="mx-2">/</span>
				<span>Settings</span>
				<span class="mx-2">/</span>
				<span>Secrets</span>
			</nav>
			<h2 class="text-xl font-semibold">Workflow Secrets</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				Encrypted environment variables for CI workflows
			</p>
		</div>
		<button class="btn btn-primary" onclick={() => (showAddModal = true)}>
			Add Secret
		</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-700 dark:text-red-400 mb-4">{error}</p>
			<button class="btn" onclick={loadSecrets}>Retry</button>
		</div>
	{:else if secrets.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🔐</div>
			<p class="text-kai-text-muted mb-4">No secrets configured</p>
			<p class="text-kai-text-muted text-sm mb-4">
				Add secrets to use in your CI workflows via <code class="bg-kai-bg-tertiary px-2 py-1 rounded">$&#123;&#123; secrets.NAME &#125;&#125;</code>
			</p>
			<button class="btn btn-primary" onclick={() => (showAddModal = true)}>
				Add your first secret
			</button>
		</div>
	{:else}
		<div class="card p-0">
			<table class="w-full">
				<thead>
					<tr class="border-b border-kai-border text-left text-kai-text-muted text-sm">
						<th class="px-4 py-3 font-medium">Name</th>
						<th class="px-4 py-3 font-medium">Scope</th>
						<th class="px-4 py-3 font-medium">Updated</th>
						<th class="px-4 py-3 font-medium w-24"></th>
					</tr>
				</thead>
				<tbody>
					{#each secrets as secret}
						<tr class="border-b border-kai-border last:border-b-0 hover:bg-kai-bg-tertiary/50">
							<td class="px-4 py-3">
								<code class="font-mono text-sm">{secret.name}</code>
							</td>
							<td class="px-4 py-3">
								<span class="px-2 py-0.5 rounded text-xs font-medium {secret.scope === 'org' ? 'bg-purple-600/10 dark:bg-purple-500/20 text-purple-700 dark:text-purple-400' : 'bg-blue-600/10 dark:bg-blue-500/20 text-blue-700 dark:text-blue-400'}">
									{secret.scope}
								</span>
							</td>
							<td class="px-4 py-3 text-kai-text-muted text-sm">
								{formatDate(secret.updated_at)}
							</td>
							<td class="px-4 py-3 text-right">
								{#if deleteConfirm === secret.name}
									<button
										class="btn btn-danger btn-sm mr-2"
										onclick={() => deleteSecret(secret.name)}
									>
										Confirm
									</button>
									<button
										class="btn btn-secondary btn-sm"
										onclick={() => (deleteConfirm = null)}
									>
										Cancel
									</button>
								{:else}
									<button
										class="text-kai-text-muted hover:text-red-700 dark:hover:text-red-400 transition-colors"
										onclick={() => (deleteConfirm = secret.name)}
									>
										<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
											<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
										</svg>
									</button>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<!-- Add Secret Modal -->
{#if showAddModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={() => (showAddModal = false)}
		onkeydown={(e) => e.key === 'Escape' && (showAddModal = false)}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-md w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
		>
			<h3 class="text-lg font-semibold mb-4">Add Secret</h3>

			<div class="mb-4">
				<label for="secret-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="secret-name"
					bind:value={newSecretName}
					class="input font-mono"
					placeholder="MY_SECRET_NAME"
					autocomplete="off"
				/>
				{#if newSecretName && !isValidName(newSecretName)}
					<p class="text-red-700 dark:text-red-400 text-xs mt-1">Use only letters, numbers, and underscores. Must start with a letter or underscore.</p>
				{/if}
			</div>

			<div class="mb-6">
				<label for="secret-value" class="block mb-2 font-medium">Value</label>
				<textarea
					id="secret-value"
					bind:value={newSecretValue}
					class="input font-mono h-24 resize-none"
					placeholder="Enter secret value..."
				></textarea>
				<p class="text-kai-text-muted text-xs mt-1">Secret values are encrypted and never displayed after saving.</p>
			</div>

			<div class="flex justify-end gap-3">
				<button class="btn btn-secondary" onclick={() => (showAddModal = false)}>
					Cancel
				</button>
				<button
					class="btn btn-primary"
					onclick={addSecret}
					disabled={saving || !newSecretName.trim() || !newSecretValue.trim() || !isValidName(newSecretName)}
				>
					{saving ? 'Saving...' : 'Add Secret'}
				</button>
			</div>
		</div>
	</div>
{/if}
