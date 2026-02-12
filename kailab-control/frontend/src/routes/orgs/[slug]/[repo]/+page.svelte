<script>
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { currentUser, currentOrg, currentRepo } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';
	import { marked } from 'marked';

	// Configure marked for GitHub-style markdown
	marked.setOptions({
		gfm: true,
		breaks: true
	});

	/**
	 * Sanitize HTML by removing potentially dangerous tags and attributes
	 */
	function sanitizeHtml(html) {
		if (!html) return '';
		return html
			.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
			.replace(/<iframe\b[^<]*(?:(?!<\/iframe>)<[^<]*)*<\/iframe>/gi, '')
			.replace(/<object\b[^<]*(?:(?!<\/object>)<[^<]*)*<\/object>/gi, '')
			.replace(/<embed\b[^>]*>/gi, '')
			.replace(/\bon\w+\s*=/gi, 'data-removed=')
			.replace(/javascript:/gi, 'removed:');
	}

	function base64ToUtf8(b64) {
		const binaryStr = atob(b64);
		const bytes = new Uint8Array(binaryStr.length);
		for (let i = 0; i < binaryStr.length; i++) {
			bytes[i] = binaryStr.charCodeAt(i);
		}
		return new TextDecoder().decode(bytes);
	}

	function safeMarkdown(content) {
		const renderer = new marked.Renderer();
		const defaultImageRenderer = renderer.image.bind(renderer);
		renderer.image = function({ href, title, text }) {
			if (href && !href.startsWith('http://') && !href.startsWith('https://') && !href.startsWith('data:')) {
				const cleanPath = href.replace(/^\.\//, '');
				const digest = fileDigestMap[cleanPath];
				if (digest) {
					href = `/${slug}/${repo}/v1/raw/${digest}`;
				}
			}
			return defaultImageRenderer({ href, title, text });
		};
		renderer.code = function({ text, lang }) {
			const escaped = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
			const langClass = lang ? ` class="language-${lang}"` : '';
			return `<div class="code-block-wrapper"><button class="copy-code-btn" title="Copy code"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg></button><pre><code${langClass}>${escaped}</code></pre></div>`;
		};
		return sanitizeHtml(marked(content, { renderer }));
	}

	function handleMarkdownClick(e) {
		const btn = e.target.closest('.copy-code-btn');
		if (!btn) return;
		const wrapper = btn.closest('.code-block-wrapper');
		const code = wrapper?.querySelector('code');
		if (!code) return;
		navigator.clipboard.writeText(code.textContent).then(() => {
			btn.classList.add('copied');
			btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
			setTimeout(() => {
				btn.classList.remove('copied');
				btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>';
			}, 2000);
		});
	}

	function isReadme(path) {
		const filename = path.split('/').pop()?.toLowerCase() || '';
		return filename === 'readme.md' || filename === 'readme' || filename === 'readme.txt' || filename === 'readme.markdown';
	}

	let { slug, repo } = $page.params;

	let loading = $state(true);
	let error = $state(null);
	let readmeContent = $state('');
	let readmeFile = $state(null);
	let repoInfo = $state(null);
	let latestSnapshot = $state(null);
	let fileCount = $state(0);
	let fileDigestMap = $state({});

	onMount(async () => {
		await loadUser();
		await loadRepoData();
	});

	async function loadRepoData() {
		loading = true;
		error = null;

		try {
			// Load org and repo info
			const orgData = await api('GET', `/api/v1/orgs/${slug}`);
			if (!orgData || orgData.error) {
				error = 'Organization not found';
				loading = false;
				return;
			}
			currentOrg.set(orgData);

			const repoData = await api('GET', `/api/v1/orgs/${slug}/repos/${repo}`);
			if (!repoData || repoData.error) {
				error = 'Repository not found';
				loading = false;
				return;
			}
			repoInfo = repoData;
			currentRepo.set(repoData);

			// Get latest snapshot ref
			const refsData = await api('GET', `/${slug}/${repo}/v1/refs?prefix=snap.`);
			if (refsData?.refs && refsData.refs.length > 0) {
				// Find snap.latest or most recent
				const latestRef = refsData.refs.find(r => r.name === 'snap.latest') || refsData.refs[0];
				if (latestRef) {
					// Decode base64 target to hex
					const targetBytes = atob(latestRef.target);
					latestSnapshot = Array.from(targetBytes, b => b.charCodeAt(0).toString(16).padStart(2, '0')).join('');
				}
			}

			if (latestSnapshot) {
				// Load files to find README
				const filesData = await api('GET', `/${slug}/${repo}/v1/files/${latestSnapshot}`);
				if (filesData?.files) {
					fileCount = filesData.files.length;
					// Build path->digest map for resolving image URLs in markdown
					const map = {};
					for (const f of filesData.files) {
						map[f.path] = f.digest;
					}
					fileDigestMap = map;
					const readme = filesData.files.find(f => isReadme(f.path));
					if (readme) {
						readmeFile = readme;
						// Load README content
						const contentData = await api('GET', `/${slug}/${repo}/v1/content/${readme.digest}`);
						if (contentData?.content) {
							try {
								readmeContent = base64ToUtf8(contentData.content);
							} catch {
								readmeContent = '';
							}
						}
					}
				}
			}
		} catch (e) {
			console.error('Failed to load repo data:', e);
			error = 'Failed to load repository';
		}

		loading = false;
	}
</script>

<svelte:head>
	<title>{repo} - {slug} | Kailab</title>
</svelte:head>

<div class="max-w-6xl mx-auto px-4 py-6">
	<!-- Breadcrumb -->
	<div class="flex items-center gap-2 text-sm text-kai-text-muted mb-4">
		<a href="/orgs/{slug}" class="hover:text-kai-text">{slug}</a>
		<span>/</span>
		<span class="text-kai-text font-medium">{repo}</span>
		{#if repoInfo?.visibility === 'public'}
			<span class="ml-2 px-2 py-0.5 text-xs rounded-full border border-kai-border text-kai-text-muted">Public</span>
		{/if}
	</div>

	<!-- Repo header -->
	<div class="flex items-center justify-between mb-6">
		<h1 class="text-2xl font-bold">{repo}</h1>
		<div class="flex items-center gap-3">
			<a href="/orgs/{slug}/{repo}/files/snap.latest" class="btn btn-secondary">
				<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
				</svg>
				Files
			</a>
			<a href="/orgs/{slug}/{repo}/reviews" class="btn btn-secondary">
				<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
				</svg>
				Reviews
			</a>
			<a href="/orgs/{slug}/{repo}/commits" class="btn btn-secondary">
				<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
				History
			</a>
			<a href="/orgs/{slug}/{repo}/workflows" class="btn btn-secondary">
				<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
				CI
			</a>
			<a href="/orgs/{slug}/{repo}/settings" class="btn btn-secondary" title="Settings">
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
				</svg>
			</a>
		</div>
	</div>

	{#if loading}
		<div class="card p-8 text-center text-kai-text-muted">
			Loading...
		</div>
	{:else if error}
		<div class="card p-8 text-center text-red-700 dark:text-red-400">
			{error}
		</div>
	{:else}
		<!-- Stats row -->
		<div class="flex items-center gap-6 mb-6 text-sm text-kai-text-muted">
			{#if fileCount > 0}
				<div class="flex items-center gap-1.5">
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
					</svg>
					<span>{fileCount} files</span>
				</div>
			{/if}
		</div>

		<!-- README -->
		{#if readmeContent}
			<div class="card">
				<div class="flex items-center gap-2 px-4 py-3 border-b border-kai-border">
					<svg class="w-4 h-4 text-kai-text-muted" fill="none" stroke="currentColor" viewBox="0 0 24 24">
						<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
					</svg>
					<span class="font-medium">{readmeFile?.path || 'README.md'}</span>
				</div>
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="markdown-body p-6" onclick={handleMarkdownClick}>
					{@html safeMarkdown(readmeContent)}
				</div>
			</div>
		{:else if latestSnapshot}
			<div class="card p-8 text-center text-kai-text-muted">
				<p>No README found in this repository.</p>
				<a href="/orgs/{slug}/{repo}/files/snap.latest" class="text-kai-accent hover:underline mt-2 inline-block">
					Browse files
				</a>
			</div>
		{:else}
			<div class="card p-8 text-center text-kai-text-muted">
				<p>This repository is empty.</p>
				<p class="mt-2 text-sm">Push some content to get started.</p>
			</div>
		{/if}
	{/if}
</div>

