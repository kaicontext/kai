<script>
	import { goto } from '$app/navigation';
	import { accessToken, refreshToken } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';
	import { onMount } from 'svelte';

	let email = $state('');
	let magicToken = $state('');
	let stage = $state('email'); // 'email' | 'token'
	let error = $state('');

	onMount(() => {
		if ($accessToken) {
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

		accessToken.set(data.access_token);
		refreshToken.set(data.refresh_token);

		await loadUser();
		goto('/');
	}

	function showEmailForm() {
		stage = 'email';
		magicToken = '';
		error = '';
	}
</script>

<div class="max-w-md mx-auto mt-24 px-5">
	<div class="card">
		<h1 class="text-center text-2xl font-semibold mb-6">Kailab</h1>
		<p class="text-center text-kai-text-muted mb-6">
			Sign in to manage your semantic version control
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
