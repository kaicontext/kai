<script>
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';

	// Redirect to the full repo view (commits tab is default)
	$effect(() => {
		goto(`/orgs/${$page.params.slug}/${$page.params.repo}/commits`, { replaceState: true });
	});
</script>

<div class="flex items-center justify-center min-h-screen">
	<p class="text-kai-text-muted">Loading...</p>
</div>
