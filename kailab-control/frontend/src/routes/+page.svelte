<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { currentUser, accessToken } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let orgs = $state([]);
	let loading = $state(true);
	let showCreateModal = $state(false);
	let newOrgSlug = $state('');
	let newOrgName = $state('');

	onMount(async () => {
		if (!$accessToken) {
			goto('/login');
			return;
		}

		if (!$currentUser) {
			await loadUser();
		}

		await loadOrgs();
	});

	async function loadOrgs() {
		loading = true;
		const data = await api('GET', '/api/v1/orgs');
		orgs = data.orgs || [];
		loading = false;
	}

	async function createOrg() {
		const data = await api('POST', '/api/v1/orgs', {
			slug: newOrgSlug,
			name: newOrgName || newOrgSlug
		});

		if (data.error) {
			alert(data.error);
			return;
		}

		showCreateModal = false;
		newOrgSlug = '';
		newOrgName = '';
		await loadOrgs();
	}

	function selectOrg(slug) {
		goto(`/orgs/${slug}`);
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<h2 class="text-xl font-semibold">Your Organizations</h2>
		<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
			New Organization
		</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if orgs.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">📁</div>
			<p class="text-kai-text-muted mb-4">No organizations yet</p>
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
				Create your first organization
			</button>
		</div>
	{:else}
		<div class="card p-0">
			{#each orgs as org}
				<button class="list-item w-full text-left" onclick={() => selectOrg(org.slug)}>
					<div>
						<span class="font-medium text-kai-accent">{org.name}</span>
						<span class="text-kai-text-muted ml-2">/{org.slug}</span>
					</div>
					<span class="badge">{org.plan}</span>
				</button>
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
			<h3 class="text-lg font-semibold mb-4">Create Organization</h3>
			<div class="mb-4">
				<label for="org-slug" class="block mb-2 font-medium">Slug</label>
				<input
					type="text"
					id="org-slug"
					bind:value={newOrgSlug}
					class="input"
					placeholder="my-org"
					pattern="[a-z0-9._-]+"
				/>
				<small class="text-kai-text-muted">Lowercase letters, numbers, hyphens, underscores</small>
			</div>
			<div class="mb-4">
				<label for="org-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="org-name"
					bind:value={newOrgName}
					class="input"
					placeholder="My Organization"
				/>
			</div>
			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={() => (showCreateModal = false)}>Cancel</button>
				<button class="btn btn-primary" onclick={createOrg}>Create</button>
			</div>
		</div>
	</div>
{/if}
