<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api, loadUser } from '$lib/api.js';

	let variables = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showAddModal = $state(false);
	let newVarName = $state('');
	let newVarValue = $state('');
	let saving = $state(false);
	let deleteConfirm = $state(null);
	let editingVar = $state(null);
	let editValue = $state('');

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}
		await loadVariables();
	});

	async function loadVariables() {
		loading = true;
		error = '';
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}/variables`);
			if (data.error) {
				error = data.error;
				variables = [];
			} else {
				variables = data.variables || [];
			}
		} catch (e) {
			error = 'Failed to load variables';
			variables = [];
		}

		loading = false;
	}

	async function addVariable() {
		if (!newVarName.trim()) return;

		saving = true;
		const { slug, repo } = $page.params;

		try {
			const result = await api('PUT', `/api/v1/orgs/${slug}/repos/${repo}/variables/${newVarName}`, {
				value: newVarValue
			});

			if (result.error) {
				error = result.error;
			} else {
				showAddModal = false;
				newVarName = '';
				newVarValue = '';
				await loadVariables();
			}
		} catch (e) {
			error = 'Failed to save variable';
		}

		saving = false;
	}

	async function updateVariable() {
		if (!editingVar) return;

		saving = true;
		const { slug, repo } = $page.params;

		try {
			const result = await api('PUT', `/api/v1/orgs/${slug}/repos/${repo}/variables/${editingVar}`, {
				value: editValue
			});

			if (result.error) {
				error = result.error;
			} else {
				editingVar = null;
				editValue = '';
				await loadVariables();
			}
		} catch (e) {
			error = 'Failed to update variable';
		}

		saving = false;
	}

	async function deleteVariable(name) {
		const { slug, repo } = $page.params;

		try {
			await api('DELETE', `/api/v1/orgs/${slug}/repos/${repo}/variables/${name}`);
			deleteConfirm = null;
			await loadVariables();
		} catch (e) {
			error = 'Failed to delete variable';
		}
	}

	function startEdit(v) {
		editingVar = v.name;
		editValue = v.value;
	}

	function isValidName(name) {
		return /^[A-Z_][A-Z0-9_]*$/i.test(name);
	}
</script>

<div class="max-w-4xl mx-auto px-5 py-8">
	<div class="flex gap-6">
		<div class="w-48 shrink-0">
			<nav class="space-y-1">
				<a
					href="/orgs/{$page.params.slug}/{$page.params.repo}/settings"
					class="block px-3 py-2 rounded-md text-sm font-medium text-kai-text-muted hover:text-kai-text hover:bg-kai-bg-tertiary"
				>
					General
				</a>
				<a
					href="/orgs/{$page.params.slug}/{$page.params.repo}/settings/secrets"
					class="block px-3 py-2 rounded-md text-sm font-medium text-kai-text-muted hover:text-kai-text hover:bg-kai-bg-tertiary"
				>
					Secrets
				</a>
				<a
					href="/orgs/{$page.params.slug}/{$page.params.repo}/settings/variables"
					class="block px-3 py-2 rounded-md text-sm font-medium bg-kai-bg-tertiary text-kai-text"
				>
					Variables
				</a>
				<a
					href="/orgs/{$page.params.slug}/{$page.params.repo}/webhooks"
					class="block px-3 py-2 rounded-md text-sm font-medium text-kai-text-muted hover:text-kai-text hover:bg-kai-bg-tertiary"
				>
					Webhooks
				</a>
			</nav>
		</div>

		<div class="flex-1 min-w-0">
			<div class="flex justify-between items-center mb-6">
				<div>
					<h2 class="text-xl font-semibold">Variables</h2>
					<p class="text-kai-text-muted text-sm mt-1">
						Environment variables for CI workflows, accessible via <code class="bg-kai-bg-tertiary px-2 py-1 rounded">$&#123;&#123; vars.NAME &#125;&#125;</code>
					</p>
				</div>
				<button class="btn btn-primary" onclick={() => (showAddModal = true)}>
					Add Variable
				</button>
			</div>

			{#if loading}
				<div class="text-center py-12 text-kai-text-muted">Loading...</div>
			{:else if error}
				<div class="card text-center py-12">
					<p class="text-red-400 mb-4">{error}</p>
					<button class="btn" onclick={loadVariables}>Retry</button>
				</div>
			{:else if variables.length === 0}
				<div class="card text-center py-12">
					<p class="text-kai-text-muted mb-4">No variables configured</p>
					<p class="text-kai-text-muted text-sm mb-4">
						Unlike secrets, variable values are visible and not encrypted.
					</p>
					<button class="btn btn-primary" onclick={() => (showAddModal = true)}>
						Add your first variable
					</button>
				</div>
			{:else}
				<div class="card p-0">
					<table class="w-full">
						<thead>
							<tr class="border-b border-kai-border text-left text-kai-text-muted text-sm">
								<th class="px-4 py-3 font-medium">Name</th>
								<th class="px-4 py-3 font-medium">Value</th>
								<th class="px-4 py-3 font-medium">Scope</th>
								<th class="px-4 py-3 font-medium w-32"></th>
							</tr>
						</thead>
						<tbody>
							{#each variables as v}
								<tr class="border-b border-kai-border last:border-b-0 hover:bg-kai-bg-tertiary/50">
									<td class="px-4 py-3">
										<code class="font-mono text-sm">{v.name}</code>
									</td>
									<td class="px-4 py-3">
										{#if editingVar === v.name}
											<div class="flex items-center gap-2">
												<input
													type="text"
													bind:value={editValue}
													class="input font-mono text-sm flex-1"
												/>
												<button class="btn btn-primary btn-sm" onclick={updateVariable} disabled={saving}>
													Save
												</button>
												<button class="btn btn-secondary btn-sm" onclick={() => (editingVar = null)}>
													Cancel
												</button>
											</div>
										{:else}
											<code class="font-mono text-sm text-kai-text-muted">{v.value}</code>
										{/if}
									</td>
									<td class="px-4 py-3">
										<span class="px-2 py-0.5 rounded text-xs font-medium {v.scope === 'org' ? 'bg-purple-500/20 text-purple-400' : 'bg-blue-500/20 text-blue-400'}">
											{v.scope}
										</span>
									</td>
									<td class="px-4 py-3 text-right">
										{#if deleteConfirm === v.name}
											<button
												class="btn btn-danger btn-sm mr-2"
												onclick={() => deleteVariable(v.name)}
											>
												Confirm
											</button>
											<button
												class="btn btn-secondary btn-sm"
												onclick={() => (deleteConfirm = null)}
											>
												Cancel
											</button>
										{:else if editingVar !== v.name}
											<button
												class="text-kai-text-muted hover:text-kai-text transition-colors mr-2"
												onclick={() => startEdit(v)}
												title="Edit"
											>
												<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
													<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
												</svg>
											</button>
											<button
												class="text-kai-text-muted hover:text-red-400 transition-colors"
												onclick={() => (deleteConfirm = v.name)}
												title="Delete"
											>
												<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
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
	</div>
</div>

<!-- Add Variable Modal -->
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
			<h3 class="text-lg font-semibold mb-4">Add Variable</h3>

			<div class="mb-4">
				<label for="var-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="var-name"
					bind:value={newVarName}
					class="input font-mono"
					placeholder="MY_VARIABLE"
					autocomplete="off"
				/>
				{#if newVarName && !isValidName(newVarName)}
					<p class="text-red-400 text-xs mt-1">Use only letters, numbers, and underscores. Must start with a letter or underscore.</p>
				{/if}
			</div>

			<div class="mb-6">
				<label for="var-value" class="block mb-2 font-medium">Value</label>
				<input
					type="text"
					id="var-value"
					bind:value={newVarValue}
					class="input font-mono"
					placeholder="Enter value..."
				/>
				<p class="text-kai-text-muted text-xs mt-1">Variable values are visible to anyone with read access to this repository.</p>
			</div>

			<div class="flex justify-end gap-3">
				<button class="btn btn-secondary" onclick={() => (showAddModal = false)}>
					Cancel
				</button>
				<button
					class="btn btn-primary"
					onclick={addVariable}
					disabled={saving || !newVarName.trim() || !isValidName(newVarName)}
				>
					{saving ? 'Saving...' : 'Add Variable'}
				</button>
			</div>
		</div>
	</div>
{/if}
