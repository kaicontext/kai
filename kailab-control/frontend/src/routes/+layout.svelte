<script>
	import '../app.css';
	import { currentUser } from '$lib/stores.js';
	import { loadUser, logout } from '$lib/api.js';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';

	let { children } = $props();
	let showUserMenu = $state(false);

	onMount(async () => {
		// Try to load user from cookie-based session
		if (!$currentUser) {
			await loadUser();
		}
	});

	function handleLogout() {
		showUserMenu = false;
		logout();
	}

	function goToDashboard() {
		goto('/');
	}

	function goToTokens() {
		showUserMenu = false;
		goto('/tokens');
	}

	function handleClickOutside(event) {
		if (showUserMenu && !event.target.closest('.user-menu-container')) {
			showUserMenu = false;
		}
	}
</script>

<svelte:window onclick={handleClickOutside} />

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
				<!-- User avatar dropdown -->
				<div class="relative user-menu-container">
					<button
						onclick={() => showUserMenu = !showUserMenu}
						class="w-8 h-8 rounded-full bg-kai-accent flex items-center justify-center font-semibold text-sm hover:ring-2 hover:ring-kai-accent/50 transition-all"
					>
						{$currentUser.email[0].toUpperCase()}
					</button>
					{#if showUserMenu}
						<div class="absolute right-0 mt-2 w-48 bg-kai-bg-secondary border border-kai-border rounded-md shadow-lg py-1 z-50">
							<div class="px-4 py-2 border-b border-kai-border">
								<p class="text-sm font-medium truncate">{$currentUser.email}</p>
							</div>
							<button
								onclick={goToTokens}
								class="w-full text-left px-4 py-2 text-sm text-kai-text hover:bg-kai-bg transition-colors"
							>
								API Tokens
							</button>
							<button
								onclick={handleLogout}
								class="w-full text-left px-4 py-2 text-sm text-red-400 hover:bg-kai-bg transition-colors"
							>
								Logout
							</button>
						</div>
					{/if}
				</div>
			</nav>
		</div>
	</header>
{/if}

<main>
	{@render children()}
</main>
