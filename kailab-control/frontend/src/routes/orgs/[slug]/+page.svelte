<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser, currentOrg } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let repos = $state([]);
	let members = $state([]);
	let loading = $state(true);
	let activeTab = $state('repos');
	let showCreateModal = $state(false);
	let showAddMemberModal = $state(false);
	let newRepoName = $state('');
	let newRepoVisibility = $state('private');
	let newMemberEmail = $state('');
	let newMemberRole = $state('developer');

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
		await loadMembers();
	});

	let error = $state(null);

	async function loadRepos() {
		loading = true;
		error = null;
		const data = await api('GET', `/api/v1/orgs/${$page.params.slug}/repos`);
		if (data.error) {
			error = data.error;
			loading = false;
			return;
		}
		repos = data.repos || [];
		loading = false;
	}

	async function loadMembers() {
		const data = await api('GET', `/api/v1/orgs/${$page.params.slug}/members`);
		if (!data.error) {
			members = data.members || [];
		}
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

	async function addMember() {
		if (!newMemberEmail.trim()) return;

		const data = await api('POST', `/api/v1/orgs/${$page.params.slug}/members`, {
			email: newMemberEmail,
			role: newMemberRole
		});

		if (data.error) {
			alert(data.error);
			return;
		}

		showAddMemberModal = false;
		newMemberEmail = '';
		newMemberRole = 'developer';
		await loadMembers();
	}

	async function removeMember(userId) {
		if (!confirm('Are you sure you want to remove this member?')) return;

		const data = await api('DELETE', `/api/v1/orgs/${$page.params.slug}/members/${userId}`);
		if (data.error) {
			alert(data.error);
			return;
		}
		await loadMembers();
	}

	function selectRepo(name) {
		goto(`/orgs/${$page.params.slug}/${name}`);
	}

	function getRoleBadgeColor(role) {
		switch (role) {
			case 'owner': return 'bg-purple-500/20 text-purple-400';
			case 'admin': return 'bg-red-500/20 text-red-400';
			case 'maintainer': return 'bg-orange-500/20 text-orange-400';
			case 'developer': return 'bg-blue-500/20 text-blue-400';
			case 'reporter': return 'bg-gray-500/20 text-gray-400';
			default: return 'bg-gray-500/20 text-gray-400';
		}
	}
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	<div class="flex justify-between items-center mb-6">
		<h2 class="text-xl font-semibold">{$page.params.slug}</h2>
		{#if activeTab === 'repos'}
			<button class="btn btn-primary" onclick={() => (showCreateModal = true)}>New Repository</button>
		{:else if activeTab === 'members'}
			<button class="btn btn-primary" onclick={() => (showAddMemberModal = true)}>Add Member</button>
		{/if}
	</div>

	<!-- Tabs -->
	<div class="border-b border-kai-border mb-6">
		<nav class="flex gap-4">
			<button
				class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'repos' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
				onclick={() => activeTab = 'repos'}
			>
				Repositories
				{#if repos.length > 0}
					<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{repos.length}</span>
				{/if}
			</button>
			<button
				class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'members' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
				onclick={() => activeTab = 'members'}
			>
				Members
				{#if members.length > 0}
					<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{members.length}</span>
				{/if}
			</button>
		</nav>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">🔒</div>
			<p class="text-kai-text-muted mb-2">Organization not found or access denied</p>
			<p class="text-sm text-kai-text-muted mb-4">{error}</p>
			<a href="/" class="btn btn-primary">Go Home</a>
		</div>
	{:else if activeTab === 'repos'}
		{#if repos.length === 0}
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
	{:else if activeTab === 'members'}
		{#if members.length === 0}
			<div class="card text-center py-12">
				<div class="text-5xl mb-4">👥</div>
				<p class="text-kai-text-muted mb-4">No members yet</p>
				<button class="btn btn-primary" onclick={() => (showAddMemberModal = true)}>
					Add your first member
				</button>
			</div>
		{:else}
			<div class="card p-0">
				{#each members as member}
					<div class="list-item flex items-center justify-between">
						<div class="flex items-center gap-3">
							<div class="w-8 h-8 rounded-full bg-kai-accent/20 flex items-center justify-center text-sm font-medium text-kai-accent">
								{member.name?.[0]?.toUpperCase() || member.email[0].toUpperCase()}
							</div>
							<div>
								<div class="font-medium">{member.name || member.email}</div>
								{#if member.name}
									<div class="text-sm text-kai-text-muted">{member.email}</div>
								{/if}
							</div>
						</div>
						<div class="flex items-center gap-3">
							<span class="px-2 py-1 text-xs rounded-full {getRoleBadgeColor(member.role)}">{member.role}</span>
							{#if member.role !== 'owner'}
								<button
									class="text-kai-text-muted hover:text-red-400 transition-colors"
									onclick={() => removeMember(member.user_id)}
									title="Remove member"
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
										<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
									</svg>
								</button>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		{/if}
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

{#if showAddMemberModal}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={() => (showAddMemberModal = false)}
		onkeydown={(e) => e.key === 'Escape' && (showAddMemberModal = false)}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-md w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
		>
			<h3 class="text-lg font-semibold mb-4">Add Member</h3>
			<div class="mb-4">
				<label for="member-email" class="block mb-2 font-medium">Email</label>
				<input
					type="email"
					id="member-email"
					bind:value={newMemberEmail}
					class="input"
					placeholder="user@example.com"
				/>
			</div>
			<div class="mb-4">
				<label for="member-role" class="block mb-2 font-medium">Role</label>
				<select id="member-role" bind:value={newMemberRole} class="input">
					<option value="reporter">Reporter (read-only)</option>
					<option value="developer">Developer (push snapshots)</option>
					<option value="maintainer">Maintainer (manage repos)</option>
					<option value="admin">Admin (manage members)</option>
				</select>
			</div>
			<div class="flex justify-end gap-2 mt-6">
				<button class="btn" onclick={() => (showAddMemberModal = false)}>Cancel</button>
				<button class="btn btn-primary" onclick={addMember}>Add Member</button>
			</div>
		</div>
	</div>
{/if}
