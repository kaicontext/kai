<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';

  let status = 'Verifying...';
  let error = null;

  onMount(async () => {
    const token = $page.url.searchParams.get('token');

    if (!token) {
      error = 'No token provided';
      status = 'Error';
      return;
    }

    try {
      const res = await fetch('/api/v1/auth/token', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ magic_token: token }),
        credentials: 'include'
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to verify token');
      }

      status = 'Success! Redirecting...';

      // Redirect to home or dashboard
      setTimeout(() => goto('/'), 500);
    } catch (e) {
      error = e.message;
      status = 'Error';
    }
  });
</script>

<div class="min-h-screen flex items-center justify-center bg-kai-bg">
  <div class="max-w-md w-full p-8 bg-kai-surface rounded-lg shadow-lg text-center">
    {#if error}
      <div class="text-red-500 mb-4">
        <svg class="w-16 h-16 mx-auto mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <h1 class="text-xl font-semibold mb-2">Verification Failed</h1>
        <p class="text-kai-text-muted">{error}</p>
      </div>
      <a href="/login" class="inline-block mt-4 px-4 py-2 bg-kai-accent text-white rounded hover:bg-kai-accent-hover">
        Try Again
      </a>
    {:else}
      <div class="text-kai-text">
        <svg class="w-16 h-16 mx-auto mb-4 animate-spin text-kai-accent" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
        </svg>
        <h1 class="text-xl font-semibold">{status}</h1>
      </div>
    {/if}
  </div>
</div>
