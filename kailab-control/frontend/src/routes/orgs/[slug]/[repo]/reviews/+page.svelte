<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';

	let reviews = $state([]);
	let loading = $state(true);
	let error = $state('');

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
		await loadReviews();
	});

	async function loadReviews() {
		loading = true;
		error = '';
		const { slug, repo } = $page.params;

		try {
			const data = await api('GET', `/${slug}/${repo}/v1/reviews`);
			if (data.error) {
				error = data.error;
				reviews = [];
			} else {
				reviews = (data.reviews || []).filter(review => !['merged', 'abandoned'].includes(review.state));
			}
		} catch (e) {
			error = 'Failed to load reviews';
			reviews = [];
		}

		loading = false;
	}

	function getStateColor(state) {
		switch (state) {
			case 'open':
				return 'bg-green-500/20 text-green-400';
			case 'approved':
				return 'bg-blue-500/20 text-blue-400';
			case 'changes_requested':
				return 'bg-yellow-500/20 text-yellow-400';
			case 'merged':
				return 'bg-purple-500/20 text-purple-400';
			case 'abandoned':
				return 'bg-gray-500/20 text-gray-400';
			case 'draft':
			default:
				return 'bg-gray-500/20 text-gray-400';
		}
	}

	function formatState(state) {
		return state.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
	}

	function formatDate(timestamp) {
		if (!timestamp) return '';
		return new Date(timestamp).toLocaleDateString('en-US', {
			month: 'short',
			day: 'numeric',
			year: 'numeric'
		});
	}

	function viewReview(review) {
		const { slug, repo } = $page.params;
		goto(`/orgs/${slug}/${repo}/reviews/${review.id}`);
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
				<span>Reviews</span>
			</nav>
			<h2 class="text-xl font-semibold">Code Reviews</h2>
			<p class="text-kai-text-muted text-sm mt-1">
				Semantic code reviews for changesets
			</p>
		</div>
	</div>

	{#if loading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if error}
		<div class="card text-center py-12">
			<p class="text-red-400 mb-4">{error}</p>
			<button class="btn" onclick={loadReviews}>Retry</button>
		</div>
	{:else if reviews.length === 0}
		<div class="card text-center py-12">
			<div class="text-5xl mb-4">📝</div>
			<p class="text-kai-text-muted mb-4">No reviews yet</p>
			<p class="text-kai-text-muted text-sm">
				Create reviews from the CLI with <code class="bg-kai-bg-tertiary px-2 py-1 rounded">kai review open</code>
			</p>
		</div>
	{:else}
		<div class="card p-0">
			{#each reviews as review}
				<button
					class="list-item w-full text-left hover:bg-kai-bg-tertiary transition-colors cursor-pointer"
					onclick={() => viewReview(review)}
				>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-3">
							<span class="px-2 py-0.5 rounded text-xs font-medium {getStateColor(review.state)}">
								{formatState(review.state)}
							</span>
							<span class="font-medium truncate">{review.title || 'Untitled Review'}</span>
						</div>
						<div class="text-kai-text-muted text-xs mt-1 flex items-center gap-3">
							<span>#{review.id.slice(0, 8)}</span>
							<span>by {review.author || 'unknown'}</span>
							{#if review.createdAt}
								<span>{formatDate(review.createdAt)}</span>
							{/if}
							{#if review.targetKind}
								<span class="text-kai-text-muted/60">{review.targetKind}</span>
							{/if}
						</div>
					</div>
					<div class="text-kai-text-muted">
						<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
						</svg>
					</div>
				</button>
			{/each}
		</div>
	{/if}
</div>
