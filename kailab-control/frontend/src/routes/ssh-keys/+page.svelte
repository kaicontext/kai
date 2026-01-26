<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let sshKeys = $state([]);
	let loading = $state(true);
	let showCreateModal = $state(false);
	let newKeyName = $state('');
	let newKeyPublicKey = $state('');
	let error = $state('');

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}

		await loadSSHKeys();
	});

	async function loadSSHKeys() {
		loading = true;
		const data = await api('GET', '/api/v1/me/ssh-keys');
		sshKeys = data.ssh_keys || [];
		loading = false;
	}

	async function createSSHKey() {
		error = '';

		if (!newKeyName.trim()) {
			error = 'Name is required';
			return;
		}
		if (!newKeyPublicKey.trim()) {
			error = 'Public key is required';
			return;
		}

		const data = await api('POST', '/api/v1/me/ssh-keys', {
			name: newKeyName.trim(),
			public_key: newKeyPublicKey.trim()
		});

		if (data.error) {
			error = data.error;
			return;
		}

		showCreateModal = false;
		newKeyName = '';
		newKeyPublicKey = '';
		await loadSSHKeys();
	}

	async function deleteSSHKey(id, name) {
		if (!confirm(`Are you sure you want to delete the SSH key "${name}"?`)) return;

		await api('DELETE', `/api/v1/me/ssh-keys/${id}`);
		await loadSSHKeys();
	}

	function closeModal() {
		showCreateModal = false;
		newKeyName = '';
		newKeyPublicKey = '';
		error = '';
	}

	function formatDate(dateStr) {
		if (!dateStr) return 'Never';
		return new Date(dateStr).toLocaleDateString();
	}

	function truncateFingerprint(fp) {
		if (!fp) return '';
		// Show SHA256:xxxx...xxxx format
		if (fp.length > 24) {
			return fp.slice(0, 16) + '...' + fp.slice(-8);
		}
		return fp;
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<div>
			<h2 class="text-xl font-semibold">SSH Keys</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				Manage SSH keys for Git access via <code class="text-xs bg-kai-bg-tertiary px-1 py-0.5 rounded">git@git.kailayer.com</code>
			</p>
		</div>
		<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>Add SSH Key</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if sshKeys.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🔐</div>
			<p class="text-kai-text-muted mb-4">No SSH keys yet</p>
			<p class="text-kai-text-muted text-sm mb-6">
				Add an SSH key to push and pull repositories via SSH.
			</p>
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
				Add your first SSH key
			</button>
		</div>
	{:else}
		<div class="card p-0">
			{#each sshKeys as key}
				<div class="list-item">
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2">
							<span class="font-medium">{key.name}</span>
						</div>
						<div class="text-kai-text-muted text-xs mt-1 font-mono">
							{truncateFingerprint(key.fingerprint)}
						</div>
					</div>
					<div class="flex items-center gap-4">
						<div class="text-right">
							<div class="text-kai-text-muted text-xs">
								Added {formatDate(key.created_at)}
							</div>
							<div class="text-kai-text-muted text-xs">
								{key.last_used_at ? 'Last used ' + formatDate(key.last_used_at) : 'Never used'}
							</div>
						</div>
						<button class="btn btn-danger" onclick={() => deleteSSHKey(key.id, key.name)}>
							Delete
						</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<div class="mt-8 card">
		<h3 class="font-semibold mb-3">Quick Start</h3>
		<p class="text-kai-text-muted text-sm mb-4">
			Clone a repository using SSH:
		</p>
		<div class="code-block">
			<code>git clone git@git.kailayer.com:org/repo.git</code>
		</div>
	</div>
</div>

{#if showCreateModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={closeModal}
		onkeydown={(e) => e.key === 'Escape' && closeModal()}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-lg w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
		>
			<h3 class="text-lg font-semibold mb-4">Add SSH Key</h3>

			{#if error}
				<div class="alert alert-error mb-4">{error}</div>
			{/if}

			<div class="mb-4">
				<label for="key-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="key-name"
					bind:value={newKeyName}
					class="input"
					placeholder="My laptop"
				/>
				<p class="text-kai-text-muted text-xs mt-1">A friendly name to identify this key</p>
			</div>

			<div class="mb-4">
				<label for="public-key" class="block mb-2 font-medium">Public Key</label>
				<textarea
					id="public-key"
					bind:value={newKeyPublicKey}
					class="input font-mono text-sm"
					rows="4"
					placeholder="ssh-ed25519 AAAA... or ssh-rsa AAAA..."
				></textarea>
				<p class="text-kai-text-muted text-xs mt-1">
					Paste your public key (usually found in <code class="text-xs">~/.ssh/id_ed25519.pub</code> or <code class="text-xs">~/.ssh/id_rsa.pub</code>)
				</p>
			</div>

			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={closeModal}>Cancel</button>
				<button class="btn btn-primary" onclick={createSSHKey}>Add Key</button>
			</div>
		</div>
	</div>
{/if}
