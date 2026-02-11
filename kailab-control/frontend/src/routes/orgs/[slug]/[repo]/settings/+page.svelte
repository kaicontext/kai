<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api, loadUser } from '$lib/api.js';

	let repoInfo = $state(null);
	let loading = $state(true);
	let error = $state('');

	// Rename
	let repoName = $state('');
	let renaming = $state(false);
	let renameError = $state('');
	let renameSuccess = $state('');

	// Visibility
	let visibility = $state('private');
	let savingVisibility = $state(false);
	let visibilityError = $state('');
	let visibilitySuccess = $state('');

	// Delete
	let showDeleteConfirm = $state(false);
	let deleteConfirmName = $state('');
	let deleting = $state(false);

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}
		await loadRepo();
	});

	async function loadRepo() {
		loading = true;
		error = '';
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}`);
			if (data.error) {
				error = data.error;
			} else {
				repoInfo = data;
				repoName = data.name;
				visibility = data.visibility;
			}
		} catch (e) {
			error = 'Failed to load repository';
		}

		loading = false;
	}

	async function renameRepo() {
		const trimmed = repoName.trim().toLowerCase();
		if (!trimmed || trimmed === repoInfo.name) return;

		renaming = true;
		renameError = '';
		renameSuccess = '';
		const { slug, repo } = $page.params;

		try {
			const result = await api('PATCH', `/api/v1/orgs/${slug}/repos/${repo}`, {
				name: trimmed
			});

			if (result.error) {
				renameError = result.error;
			} else {
				renameSuccess = 'Repository renamed successfully.';
				repoInfo = result;
				// Navigate to the new URL
				setTimeout(() => {
					goto(`/orgs/${slug}/${result.name}/settings`, { replaceState: true });
				}, 500);
			}
		} catch (e) {
			renameError = 'Failed to rename repository';
		}

		renaming = false;
	}

	async function updateVisibility() {
		if (visibility === repoInfo.visibility) return;

		savingVisibility = true;
		visibilityError = '';
		visibilitySuccess = '';
		const { slug, repo } = $page.params;

		try {
			const result = await api('PATCH', `/api/v1/orgs/${slug}/repos/${repo}`, {
				visibility
			});

			if (result.error) {
				visibilityError = result.error;
			} else {
				visibilitySuccess = 'Visibility updated.';
				repoInfo = result;
			}
		} catch (e) {
			visibilityError = 'Failed to update visibility';
		}

		savingVisibility = false;
	}

	async function deleteRepo() {
		deleting = true;
		const { slug, repo } = $page.params;

		try {
			const result = await api('DELETE', `/api/v1/orgs/${slug}/repos/${repo}`);
			if (result?.error) {
				error = result.error;
				deleting = false;
			} else {
				goto(`/orgs/${slug}`);
			}
		} catch (e) {
			error = 'Failed to delete repository';
			deleting = false;
		}
	}

	let deleteNameMatches = $derived(
		deleteConfirmName === `${$page.params.slug}/${$page.params.repo}`
	);
</script>

<div class="max-w-4xl mx-auto px-5 py-8">
	<!-- Breadcrumb -->
	<nav class="text-sm text-kai-text-muted mb-2">
		<a href="/orgs/{$page.params.slug}" class="hover:text-kai-text">{$page.params.slug}</a>
		<span class="mx-2">/</span>
		<a href="/orgs/{$page.params.slug}/{$page.params.repo}" class="hover:text-kai-text"
			>{$page.params.repo}</a
		>
		<span class="mx-2">/</span>
		<span>Settings</span>
	</nav>
	<h2 class="text-xl font-semibold mb-6">Repository Settings</h2>

	<!-- Settings nav -->
	<div class="flex gap-6 mb-8">
		<div class="w-48 shrink-0">
			<nav class="space-y-1">
				<a
					href="/orgs/{$page.params.slug}/{$page.params.repo}/settings"
					class="block px-3 py-2 rounded-md text-sm font-medium bg-kai-bg-tertiary text-kai-text"
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
					class="block px-3 py-2 rounded-md text-sm font-medium text-kai-text-muted hover:text-kai-text hover:bg-kai-bg-tertiary"
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
			{#if loading}
				<div class="text-center py-12 text-kai-text-muted">Loading...</div>
			{:else if error}
				<div class="card text-center py-12">
					<p class="text-red-400 mb-4">{error}</p>
					<button class="btn" onclick={loadRepo}>Retry</button>
				</div>
			{:else}
				<div class="space-y-8">
					<!-- Rename -->
					<div class="card p-5">
						<h3 class="text-base font-semibold mb-1">Repository name</h3>
						<p class="text-sm text-kai-text-muted mb-4">
							Renaming will change the URL for this repository.
						</p>
						<div class="flex items-end gap-3">
							<div class="flex-1">
								<input
									type="text"
									bind:value={repoName}
									class="input font-mono"
									placeholder="repository-name"
								/>
							</div>
							<button
								class="btn btn-primary"
								onclick={renameRepo}
								disabled={renaming || !repoName.trim() || repoName.trim().toLowerCase() === repoInfo.name}
							>
								{renaming ? 'Renaming...' : 'Rename'}
							</button>
						</div>
						{#if renameError}
							<p class="text-red-400 text-sm mt-2">{renameError}</p>
						{/if}
						{#if renameSuccess}
							<p class="text-green-400 text-sm mt-2">{renameSuccess}</p>
						{/if}
					</div>

					<!-- Visibility -->
					<div class="card p-5">
						<h3 class="text-base font-semibold mb-1">Visibility</h3>
						<p class="text-sm text-kai-text-muted mb-4">
							Control who can see this repository.
						</p>
						<div class="space-y-3 mb-4">
							<label class="flex items-start gap-3 cursor-pointer">
								<input
									type="radio"
									name="visibility"
									value="private"
									bind:group={visibility}
									class="mt-1"
								/>
								<div>
									<div class="text-sm font-medium">Private</div>
									<div class="text-xs text-kai-text-muted">Only org members with access can see this repository.</div>
								</div>
							</label>
							<label class="flex items-start gap-3 cursor-pointer">
								<input
									type="radio"
									name="visibility"
									value="internal"
									bind:group={visibility}
									class="mt-1"
								/>
								<div>
									<div class="text-sm font-medium">Internal</div>
									<div class="text-xs text-kai-text-muted">All members of the organization can see this repository.</div>
								</div>
							</label>
							<label class="flex items-start gap-3 cursor-pointer">
								<input
									type="radio"
									name="visibility"
									value="public"
									bind:group={visibility}
									class="mt-1"
								/>
								<div>
									<div class="text-sm font-medium">Public</div>
									<div class="text-xs text-kai-text-muted">Anyone can see this repository.</div>
								</div>
							</label>
						</div>
						<button
							class="btn btn-primary"
							onclick={updateVisibility}
							disabled={savingVisibility || visibility === repoInfo.visibility}
						>
							{savingVisibility ? 'Saving...' : 'Update visibility'}
						</button>
						{#if visibilityError}
							<p class="text-red-400 text-sm mt-2">{visibilityError}</p>
						{/if}
						{#if visibilitySuccess}
							<p class="text-green-400 text-sm mt-2">{visibilitySuccess}</p>
						{/if}
					</div>

					<!-- Danger Zone -->
					<div class="border border-red-500/30 rounded-xl p-5">
						<h3 class="text-base font-semibold text-red-400 mb-4">Danger Zone</h3>
						<div class="flex items-center justify-between">
							<div>
								<p class="text-sm font-medium">Delete this repository</p>
								<p class="text-sm text-kai-text-muted">Once deleted, it cannot be recovered.</p>
							</div>
							<button
								class="btn btn-danger text-sm"
								onclick={() => showDeleteConfirm = true}
							>
								Delete Repository
							</button>
						</div>
					</div>
				</div>
			{/if}
		</div>
	</div>
</div>

<!-- Delete Confirmation Modal -->
{#if showDeleteConfirm}
	<div
		class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
		onclick={() => { showDeleteConfirm = false; deleteConfirmName = ''; }}
		onkeydown={(e) => e.key === 'Escape' && (showDeleteConfirm = false)}
		role="button"
		tabindex="0"
	>
		<div
			class="bg-kai-bg-secondary border border-kai-border rounded-xl p-6 max-w-md w-11/12"
			onclick={(e) => e.stopPropagation()}
			onkeydown={() => {}}
			role="dialog"
		>
			<h3 class="text-lg font-semibold mb-2">Delete Repository</h3>
			<p class="text-kai-text-muted text-sm mb-4">
				This will permanently delete <strong class="text-kai-text">{$page.params.slug}/{$page.params.repo}</strong> and all its data including workflows, secrets, and CI history.
			</p>
			<p class="text-sm mb-2">
				Type <code class="bg-kai-bg-tertiary px-1.5 py-0.5 rounded font-mono text-sm">{$page.params.slug}/{$page.params.repo}</code> to confirm:
			</p>
			<input
				type="text"
				bind:value={deleteConfirmName}
				class="input font-mono mb-4"
				placeholder="{$page.params.slug}/{$page.params.repo}"
				autocomplete="off"
			/>
			<div class="flex justify-end gap-3">
				<button
					class="btn btn-secondary"
					onclick={() => { showDeleteConfirm = false; deleteConfirmName = ''; }}
				>
					Cancel
				</button>
				<button
					class="btn btn-danger"
					onclick={deleteRepo}
					disabled={deleting || !deleteNameMatches}
				>
					{deleting ? 'Deleting...' : 'Delete this repository'}
				</button>
			</div>
		</div>
	</div>
{/if}
