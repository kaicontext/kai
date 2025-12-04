<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser, currentOrg } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let repos = $state([]);
	let loading = $state(true);
	let showCreateModal = $state(false);
	let newRepoName = $state('');
	let newRepoVisibility = $state('private');

	$effect(() => {
		currentOrg.set($page.params.slug);
	});

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}

		await loadRepos();
	});

	async function loadRepos() {
		loading = true;
		const data = await api('GET', `/api/v1/orgs/${$page.params.slug}/repos`);
		repos = data.repos || [];
		loading = false;
	}

	async function createRepo() {
		const data = await api('POST', `/api/v1/orgs/${$page.params.slug}/repos`, {
			name: newRepoName,
			visibility: newRepoVisibility
		});

		if (data.error) {
			alert(data.error);
			return;
		}

		showCreateModal = false;
		newRepoName = '';
		newRepoVisibility = 'private';
		await loadRepos();
		goto(`/orgs/${$page.params.slug}/${data.name}`);
	}

	function selectRepo(name) {
		goto(`/orgs/${$page.params.slug}/${name}`);
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<h2 class="text-xl font-semibold">{$page.params.slug}</h2>
		<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>New Repository</button>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if repos.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">📦</div>
			<p class="text-kai-text-muted mb-4">No repositories yet</p>
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>
				Create your first repository
			</button>
		</div>
	{:else}
		<div class="card p-0">
			{#each repos as repo}
				<button class="list-item w-full text-left" onclick={() => selectRepo(repo.name)}>
					<div>
						<span class="font-medium text-kai-accent">{repo.name}</span>
					</div>
					<span class="badge badge-{repo.visibility}">{repo.visibility}</span>
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
			<h3 class="text-lg font-semibold mb-4">Create Repository</h3>
			<div class="mb-4">
				<label for="repo-name" class="block mb-2 font-medium">Name</label>
				<input
					type="text"
					id="repo-name"
					bind:value={newRepoName}
					class="input"
					placeholder="my-repo"
					pattern="[a-z0-9._-]+"
				/>
				<small class="text-kai-text-muted">Lowercase letters, numbers, hyphens, underscores</small>
			</div>
			<div class="mb-4">
				<label for="repo-visibility" class="block mb-2 font-medium">Visibility</label>
				<select id="repo-visibility" bind:value={newRepoVisibility} class="input">
					<option value="private">Private</option>
					<option value="public">Public</option>
				</select>
			</div>
			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={() => (showCreateModal = false)}>Cancel</button>
				<button class="btn btn-primary" onclick={createRepo}>Create</button>
			</div>
		</div>
	</div>
{/if}
