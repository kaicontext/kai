<script>
	import '../app.css';
	import { currentUser, accessToken } from '$lib/stores.js';
	import { loadUser, logout } from '$lib/api.js';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';

	let { children } = $props();

	onMount(async () => {
		if ($accessToken && !$currentUser) {
			await loadUser();
		}
	});

	function handleLogout() {
		logout();
	}

	function goToDashboard() {
		goto('/');
	}

	function goToTokens() {
		goto('/tokens');
	}
</script>

{#if $currentUser}
	<header class="bg-kai-bg-secondary border-b border-kai-border py-4">
		<div class="max-w-6xl mx-auto px-5 flex justify-between items-center">
			<a href="/" class="text-2xl font-semibold text-kai-text hover:text-kai-accent no-underline">
				Kailab
			</a>
			<nav class="flex gap-4 items-center">
				<button
					onclick={goToDashboard}
					class="text-kai-text no-underline px-3 py-2 rounded-md hover:bg-kai-bg"
				>
					Dashboard
				</button>
				<button
					onclick={goToTokens}
					class="text-kai-text no-underline px-3 py-2 rounded-md hover:bg-kai-bg"
				>
					API Tokens
				</button>
				<div class="flex items-center gap-2">
					<div
						class="w-8 h-8 rounded-full bg-kai-accent flex items-center justify-center font-semibold text-sm"
					>
						{$currentUser.email[0].toUpperCase()}
					</div>
					<button class="btn" onclick={handleLogout}>Logout</button>
				</div>
			</nav>
		</div>
	</header>
{/if}

<main>
	{@render children()}
</main>
