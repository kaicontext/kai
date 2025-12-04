<script>
	import { goto } from '$app/navigation';
	import { currentUser } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';
	import { onMount } from 'svelte';

	let email = $state('');
	let magicToken = $state('');
	let stage = $state('email'); // 'email' | 'token'
	let error = $state('');

	onMount(async () => {
		// Check if already logged in via cookie
		const user = await loadUser();
		if (user) {
			goto('/');
		}
	});

	async function sendMagicLink() {
		if (!email) return;
		error = '';

		const data = await api('POST', '/api/v1/auth/magic-link', { email });

		if (data.error) {
			error = data.error;
			return;
		}

		stage = 'token';

		// In debug mode, the token is returned
		if (data.dev_token) {
			magicToken = data.dev_token;
		}
	}

	async function exchangeToken() {
		if (!magicToken) return;
		error = '';

		const data = await api('POST', '/api/v1/auth/token', { magic_token: magicToken });

		if (data.error) {
			error = data.error;
			return;
		}

		// Token is now set via HttpOnly cookie by the server
		await loadUser();
		goto('/');
	}

	function showEmailForm() {
		stage = 'email';
		magicToken = '';
		error = '';
	}
</script>

<div class="min-h-screen flex">
	<!-- Left side: Design principles -->
	<div class="hidden lg:flex lg:w-1/2 bg-kai-bg-secondary border-r border-kai-border flex-col justify-center px-12">
		<h1 class="text-3xl font-bold mb-2">Kai</h1>
		<p class="text-kai-text-muted mb-8">Intent-based version control</p>

		<div class="space-y-6">
			<div class="flex gap-4">
				<div class="flex-shrink-0 w-10 h-10 rounded-lg bg-kai-accent/10 flex items-center justify-center">
					<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
					</svg>
				</div>
				<div>
					<h3 class="font-semibold mb-1">Idempotent</h3>
					<p class="text-sm text-kai-text-muted">Same command, same result. No hidden state. Content-addressed and immutable.</p>
				</div>
			</div>

			<div class="flex gap-4">
				<div class="flex-shrink-0 w-10 h-10 rounded-lg bg-kai-accent/10 flex items-center justify-center">
					<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
					</svg>
				</div>
				<div>
					<h3 class="font-semibold mb-1">Fast</h3>
					<p class="text-sm text-kai-text-muted">Sub-second operations. O(1) lookups. Transfer only what's missing.</p>
				</div>
			</div>

			<div class="flex gap-4">
				<div class="flex-shrink-0 w-10 h-10 rounded-lg bg-kai-accent/10 flex items-center justify-center">
					<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z" />
					</svg>
				</div>
				<div>
					<h3 class="font-semibold mb-1">Semantic</h3>
					<p class="text-sm text-kai-text-muted">Understands meaning, not just text. "Function added" not "line 47 changed".</p>
				</div>
			</div>

			<div class="flex gap-4">
				<div class="flex-shrink-0 w-10 h-10 rounded-lg bg-kai-accent/10 flex items-center justify-center">
					<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
					</svg>
				</div>
				<div>
					<h3 class="font-semibold mb-1">Immutable</h3>
					<p class="text-sm text-kai-text-muted">Snapshots never change. Trustworthy history. Safe concurrent operations.</p>
				</div>
			</div>

			<div class="flex gap-4">
				<div class="flex-shrink-0 w-10 h-10 rounded-lg bg-kai-accent/10 flex items-center justify-center">
					<svg class="w-5 h-5 text-kai-accent" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
					</svg>
				</div>
				<div>
					<h3 class="font-semibold mb-1">Explicit</h3>
					<p class="text-sm text-kai-text-muted">Commands say what they do. No magic. No surprises.</p>
				</div>
			</div>
		</div>
	</div>

	<!-- Right side: Login form -->
	<div class="w-full lg:w-1/2 flex items-center justify-center px-5">
		<div class="w-full max-w-md">
			<div class="card">
				<h1 class="text-center text-2xl font-semibold mb-2 lg:hidden">Kai</h1>
				<h2 class="text-center text-xl font-semibold mb-6 hidden lg:block">Sign in</h2>
				<p class="text-center text-kai-text-muted mb-6 lg:hidden">
					Intent-based version control
				</p>

				{#if error}
					<div class="alert alert-error">{error}</div>
				{/if}

				{#if stage === 'email'}
					<div class="mb-4">
						<label for="email" class="block mb-2 font-medium">Email</label>
						<input
							type="email"
							id="email"
							bind:value={email}
							class="input"
							placeholder="you@example.com"
							required
						/>
					</div>
					<button class="btn btn-primary w-full" onclick={sendMagicLink}>Send Login Link</button>
				{:else}
					<div class="alert alert-success">Check your email for a login link!</div>
					<p class="text-center text-kai-text-muted mb-4">Or enter your token directly:</p>
					<div class="mb-4">
						<input
							type="text"
							bind:value={magicToken}
							class="input"
							placeholder="Paste token from email"
						/>
					</div>
					<button class="btn btn-primary w-full mb-4" onclick={exchangeToken}>Login</button>
					<p class="text-center">
						<button class="btn" onclick={showEmailForm}>Try again</button>
					</p>
				{/if}
			</div>
		</div>
	</div>
</div>
