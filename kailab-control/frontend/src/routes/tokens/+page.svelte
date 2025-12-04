<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { currentUser, accessToken } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let tokens = $state([]);
	let loading = $state(true);
	let showCreateModal = $state(false);
	let showTokenModal = $state(false);
	let newTokenName = $state('');
	let createdToken = $state('');

	onMount(async () => {
		if (!$accessToken) {
			goto('/login');
			return;
		}

		if (!$currentUser) {
			await loadUser();
		}

		await loadTokens();
	});

	async function loadTokens() {
		loading = true;
		const data = await api('GET', '/api/v1/tokens');
		tokens = data.tokens || [];
		loading = false;
	}

	async function createToken() {
		const data = await api('POST', '/api/v1/tokens', {
			name: newTokenName,
			scopes: ['repo:read', 'repo:write']
		});

		if (data.error) {
			alert(data.error);
			return;
		}

		createdToken = data.token;
		showCreateModal = false;
		showTokenModal = true;
		newTokenName = '';
	}

	async function deleteToken(id) {
		if (!confirm('Are you sure you want to delete this token?')) return;

		await api('DELETE', `/api/v1/tokens/${id}`);
		await loadTokens();
	}

	function copyAndClose() {
		navigator.clipboard.writeText(createdToken);
		showTokenModal = false;
		createdToken = '';
		loadTokens();
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<h2 class="text-xl font-semibold">API Tokens</h2>
		<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>New Token</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if tokens.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🔑</div>
			<p class="text-kai-text-muted mb-4">No API tokens yet</p>
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
				Create your first token
			</button>
		</div>
	{:else}
		<div class="card p-0">
			{#each tokens as token}
				<div class="list-item">
					<div>
						<span class="font-medium">{token.name}</span>
						<span class="text-kai-text-muted ml-2">{token.scopes.join(', ')}</span>
					</div>
					<div class="flex items-center gap-4">
						<span class="text-kai-text-muted text-xs">
							{token.last_used_at
								? 'Last used: ' + new Date(token.last_used_at).toLocaleDateString()
								: 'Never used'}
						</span>
						<button class="btn" onclick={() => deleteToken(token.id)}>Delete</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

{#if showCreateModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={() => (showCreateModal = false)}
		onkeydown={(e) => e.key === 'Escape' && (showCreateModal = false)}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-md w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
		>
			<h3 class="text-lg font-semibold mb-4">Create API Token</h3>
			<div class="mb-4">
				<label for="token-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="token-name"
					bind:value={newTokenName}
					class="input"
					placeholder="My CLI token"
				/>
			</div>
			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={() => (showCreateModal = false)}>Cancel</button>
				<button class="btn btn-primary" onclick={createToken}>Create</button>
			</div>
		</div>
	</div>
{/if}

{#if showTokenModal}
	<div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
		<div class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-md w-11/12">
			<h3 class="text-lg font-semibold mb-4">Token Created</h3>
			<div class="alert alert-success">Copy this token now! It won't be shown again.</div>
			<div class="code-block break-all">
				<code>{createdToken}</code>
			</div>
			<div class="flex justify-end mt-6">
				<button class="btn btn-primary" onclick={copyAndClose}>Copy & Close</button>
			</div>
		</div>
	</div>
{/if}
