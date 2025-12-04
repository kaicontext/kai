<script>
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { currentUser, currentOrg, currentRepo } from '$lib/stores.js';
	import { api, loadUser } from '$lib/api.js';
	import hljs from 'highlight.js/lib/core';
	// Import languages we want to support
	import javascript from 'highlight.js/lib/languages/javascript';
	import typescript from 'highlight.js/lib/languages/typescript';
	import python from 'highlight.js/lib/languages/python';
	import go from 'highlight.js/lib/languages/go';
	import json from 'highlight.js/lib/languages/json';
	import yaml from 'highlight.js/lib/languages/yaml';
	import sql from 'highlight.js/lib/languages/sql';
	import css from 'highlight.js/lib/languages/css';
	import xml from 'highlight.js/lib/languages/xml';
	import markdown from 'highlight.js/lib/languages/markdown';
	import bash from 'highlight.js/lib/languages/bash';
	import rust from 'highlight.js/lib/languages/rust';
	import java from 'highlight.js/lib/languages/java';
	import cpp from 'highlight.js/lib/languages/cpp';
	import c from 'highlight.js/lib/languages/c';
	import ruby from 'highlight.js/lib/languages/ruby';
	import php from 'highlight.js/lib/languages/php';
	import { marked } from 'marked';

	// Configure marked for GitHub-style markdown
	marked.setOptions({
		gfm: true,
		breaks: true
	});

	// Register languages
	hljs.registerLanguage('javascript', javascript);
	hljs.registerLanguage('js', javascript);
	hljs.registerLanguage('jsx', javascript);
	hljs.registerLanguage('typescript', typescript);
	hljs.registerLanguage('ts', typescript);
	hljs.registerLanguage('tsx', typescript);
	hljs.registerLanguage('python', python);
	hljs.registerLanguage('py', python);
	hljs.registerLanguage('go', go);
	hljs.registerLanguage('json', json);
	hljs.registerLanguage('yaml', yaml);
	hljs.registerLanguage('yml', yaml);
	hljs.registerLanguage('sql', sql);
	hljs.registerLanguage('css', css);
	hljs.registerLanguage('html', xml);
	hljs.registerLanguage('xml', xml);
	hljs.registerLanguage('md', markdown);
	hljs.registerLanguage('markdown', markdown);
	hljs.registerLanguage('bash', bash);
	hljs.registerLanguage('sh', bash);
	hljs.registerLanguage('shell', bash);
	hljs.registerLanguage('rust', rust);
	hljs.registerLanguage('rs', rust);
	hljs.registerLanguage('java', java);
	hljs.registerLanguage('cpp', cpp);
	hljs.registerLanguage('c', c);
	hljs.registerLanguage('ruby', ruby);
	hljs.registerLanguage('rb', ruby);
	hljs.registerLanguage('php', php);

	let repo = $state(null);
	let refs = $state([]);
	let loading = $state(true);
	let refsLoading = $state(true);
	let activeTab = $state('changes');  // Default to changes (changesets)
	let compareBase = $state('');
	let compareHead = $state('');
	let diffResult = $state(null);
	let diffLoading = $state(false);
	let showDeleteConfirm = $state(false);
	let deleting = $state(false);

	// Changeset state
	let changesetPayloads = $state({});  // Map of ref name -> payload
	let changesetsLoading = $state(false);
	let selectedChangeset = $state(null);  // Currently selected changeset for detail view
	let changesetFiles = $state({ added: [], removed: [], modified: [] });  // Files changed in selected changeset
	let changesetFilesLoading = $state(false);

	// Files tab state
	let selectedSnapshot = $state('');
	let files = $state([]);
	let filesLoading = $state(false);
	let selectedFile = $state(null);
	let fileContent = $state('');
	let fileContentRaw = $state(null); // Raw base64 for binary files
	let fileContentLoading = $state(false);
	let selectedLines = $state({ start: null, end: null });
	let codeViewerEl = $state(null);
	let expandedDirs = $state(new Set()); // Track expanded directories

	// File type detection
	const imageExtensions = ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.ico', '.tiff', '.tif'];
	const svgExtension = '.svg';
	const binaryExtensions = [
		'.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.ico', '.tiff', '.tif', '.svg',
		'.woff', '.woff2', '.ttf', '.otf', '.eot',
		'.mp3', '.mp4', '.wav', '.avi', '.mov', '.webm', '.ogg', '.flac',
		'.zip', '.tar', '.gz', '.rar', '.7z', '.bz2',
		'.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx',
		'.exe', '.dll', '.so', '.dylib', '.bin', '.o', '.a'
	];

	function getFileExtension(path) {
		if (!path) return '';
		const dot = path.lastIndexOf('.');
		return dot >= 0 ? path.substring(dot).toLowerCase() : '';
	}

	function isImageFile(path) {
		return imageExtensions.includes(getFileExtension(path));
	}

	function isSvgFile(path) {
		return getFileExtension(path) === svgExtension;
	}

	function isBinaryFile(path) {
		return binaryExtensions.includes(getFileExtension(path));
	}

	function getMimeType(path) {
		const ext = getFileExtension(path);
		const mimeTypes = {
			'.png': 'image/png',
			'.jpg': 'image/jpeg',
			'.jpeg': 'image/jpeg',
			'.gif': 'image/gif',
			'.webp': 'image/webp',
			'.bmp': 'image/bmp',
			'.ico': 'image/x-icon',
			'.tiff': 'image/tiff',
			'.tif': 'image/tiff',
			'.svg': 'image/svg+xml'
		};
		return mimeTypes[ext] || 'application/octet-stream';
	}

	function isReadme(path) {
		const filename = path.split('/').pop().toLowerCase();
		return filename === 'readme.md' || filename === 'readme' || filename === 'readme.txt' || filename === 'readme.markdown';
	}

	function isMarkdownFile(path) {
		const ext = getFileExtension(path);
		return ext === '.md' || ext === '.markdown';
	}

	// Max file size to display images (2MB)
	const MAX_IMAGE_SIZE = 2 * 1024 * 1024;

	$effect(() => {
		currentOrg.set($page.params.slug);
		currentRepo.set($page.params.repo);
	});

	// Parse path segments: /orgs/[slug]/[repo]/[tab]/[snapshot]/[...filepath]
	// Examples:
	//   /orgs/kailab/blog-engine → snapshots
	//   /orgs/kailab/blog-engine/files/snap.latest → files tab, snap.latest selected
	//   /orgs/kailab/blog-engine/files/snap.latest/src/index.js → files tab, snap.latest, src/index.js file
	function parsePathSegments() {
		const pathParam = $page.params.path;
		if (!pathParam) {
			return { tab: 'snapshots', snapshot: null, filePath: null };
		}

		const segments = Array.isArray(pathParam) ? pathParam : pathParam.split('/');
		const tab = segments[0] || 'snapshots';

		if (tab === 'files' && segments.length > 1) {
			const snapshot = segments[1];
			const filePath = segments.length > 2 ? segments.slice(2).join('/') : null;
			return { tab, snapshot, filePath };
		}

		return { tab, snapshot: null, filePath: null };
	}

	// Build URL path for navigation
	function buildPath(tab, snapshot = null, filePath = null) {
		const base = `/orgs/${$page.params.slug}/${$page.params.repo}`;
		if (tab === 'snapshots') return base;
		if (tab === 'files') {
			if (snapshot && filePath) return `${base}/files/${snapshot}/${filePath}`;
			if (snapshot) return `${base}/files/${snapshot}`;
			return `${base}/files`;
		}
		return `${base}/${tab}`;
	}

	// Set tab and navigate
	function setTab(tab) {
		activeTab = tab;
		const path = buildPath(tab, tab === 'files' ? selectedSnapshot : null);
		goto(path, { replaceState: true });

		// Auto-load files and select README when switching to files tab
		if (tab === 'files' && selectedSnapshot && files.length === 0) {
			loadFiles(selectedSnapshot);
		} else if (tab === 'files' && !selectedSnapshot && snapshots.length > 0) {
			// Auto-select first snapshot if none selected
			setSnapshot(snapshots[0].name);
		}
	}

	// Set snapshot and navigate
	function setSnapshot(snapshot) {
		selectedSnapshot = snapshot;
		const path = buildPath('files', snapshot);
		goto(path, { replaceState: true });
		loadFiles(snapshot);
	}

	// Set selected file and navigate
	function setSelectedFile(file) {
		selectedLines = { start: null, end: null }; // Clear line selection
		loadFileContent(file);
		const path = buildPath('files', selectedSnapshot, file.path);
		goto(path, { replaceState: true });
	}

	// Get the current file link for copying
	function getCurrentFileLink() {
		if (!selectedFile) return '';
		let url = `${window.location.origin}${buildPath('files', selectedSnapshot, selectedFile.path)}`;
		if (selectedLines.start) {
			url += selectedLines.end && selectedLines.end !== selectedLines.start
				? `#L${selectedLines.start}-L${selectedLines.end}`
				: `#L${selectedLines.start}`;
		}
		return url;
	}

	// Parse line selection from URL hash (e.g., #L5 or #L5-L10)
	function parseLineHash() {
		const hash = window.location.hash;
		if (!hash) return { start: null, end: null };

		const match = hash.match(/^#L(\d+)(?:-L(\d+))?$/);
		if (match) {
			const start = parseInt(match[1], 10);
			const end = match[2] ? parseInt(match[2], 10) : start;
			return { start, end: end >= start ? end : start };
		}
		return { start: null, end: null };
	}

	// Update URL hash with line selection
	function updateLineHash(start, end = null) {
		const path = buildPath('files', selectedSnapshot, selectedFile?.path);
		let hash = '';
		if (start) {
			hash = end && end !== start ? `#L${start}-L${end}` : `#L${start}`;
		}
		window.history.replaceState({}, '', path + hash);
	}

	// Handle line number click
	function handleLineClick(lineNum, event) {
		if (event.shiftKey && selectedLines.start) {
			// Range selection
			const start = Math.min(selectedLines.start, lineNum);
			const end = Math.max(selectedLines.start, lineNum);
			selectedLines = { start, end };
			updateLineHash(start, end);
		} else {
			// Single line selection
			selectedLines = { start: lineNum, end: lineNum };
			updateLineHash(lineNum);
		}
	}

	// Check if a line is selected
	function isLineSelected(lineNum) {
		if (!selectedLines.start) return false;
		return lineNum >= selectedLines.start && lineNum <= (selectedLines.end || selectedLines.start);
	}

	// Scroll to selected line
	function scrollToLine(lineNum) {
		if (!codeViewerEl) return;
		const lineEl = codeViewerEl.querySelector(`[data-line="${lineNum}"]`);
		if (lineEl) {
			lineEl.scrollIntoView({ behavior: 'smooth', block: 'center' });
		}
	}

	// Clear line selection
	function clearLineSelection() {
		selectedLines = { start: null, end: null };
		updateLineHash(null);
	}

	onMount(async () => {
		const user = await loadUser();
		if (!user) {
			goto('/login');
			return;
		}

		const { tab, snapshot, filePath } = parsePathSegments();
		const lineSelection = parseLineHash();

		if (['changes', 'workspaces', 'files', 'snapshots', 'setup'].includes(tab)) {
			activeTab = tab;
		}
		if (snapshot) {
			selectedSnapshot = snapshot;
		}

		await loadRepo();
		await loadRefs();

		// Load changeset payloads to get intents
		await loadChangesetPayloads();

		// If URL had snapshot, load files
		if (snapshot && activeTab === 'files') {
			// If we have a file path, load that file immediately while loading full list in background
			if (filePath) {
				// Load specific file first (fast)
				const filePromise = loadSingleFile(snapshot, filePath);
				// Load full file list in background (slow) - don't auto-select README since we have a specific file
				const listPromise = loadFiles(snapshot, false);

				const file = await filePromise;
				if (file) {
					expandToFile(filePath);
					await loadFileContent(file);
					// Apply line selection from hash
					if (lineSelection.start) {
						selectedLines = lineSelection;
						setTimeout(() => scrollToLine(lineSelection.start), 100);
					}
				}

				// Wait for full list to finish loading
				await listPromise;
				// Re-expand after full list loads
				if (filePath) expandToFile(filePath);
			} else {
				// No specific file - auto-select README
				await loadFiles(snapshot, true);
			}
		}
	});

	async function loadRepo() {
		loading = true;
		const data = await api('GET', `/api/v1/orgs/${$page.params.slug}/repos/${$page.params.repo}`);
		if (data.error) {
			goto(`/orgs/${$page.params.slug}`);
			return;
		}
		repo = data;
		loading = false;
	}

	async function loadRefs() {
		refsLoading = true;
		// Fetch refs from the data plane via proxy
		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/refs`);
		if (data.refs) {
			refs = data.refs;
		}
		refsLoading = false;
	}

	// Load changeset payloads to get intents
	async function loadChangesetPayloads() {
		changesetsLoading = true;
		const csRefs = refs.filter(r => r.name.startsWith('cs.'));
		const payloads = {};

		// Fetch each changeset object to get its payload
		await Promise.all(csRefs.map(async (ref) => {
			try {
				// target is base64-encoded digest, convert to hex
				const targetHex = base64ToHex(ref.target);
				const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/objects/${targetHex}`);
				if (data.payload) {
					payloads[ref.name] = typeof data.payload === 'string' ? JSON.parse(data.payload) : data.payload;
				}
			} catch (e) {
				console.error(`Failed to load changeset ${ref.name}:`, e);
			}
		}));

		changesetPayloads = payloads;
		changesetsLoading = false;
	}

	// Convert base64 to hex string
	function base64ToHex(b64) {
		try {
			const bytes = atob(b64);
			let hex = '';
			for (let i = 0; i < bytes.length; i++) {
				hex += bytes.charCodeAt(i).toString(16).padStart(2, '0');
			}
			return hex;
		} catch {
			return b64;
		}
	}

	// Load file diff for a changeset
	async function loadChangesetDiff(csRef) {
		const payload = changesetPayloads[csRef.name];
		if (!payload || !payload.base || !payload.head) {
			changesetFiles = { added: [], removed: [], modified: [] };
			return;
		}

		changesetFilesLoading = true;

		try {
			// Find snapshot refs for base and head
			const baseSnap = refs.find(r => r.target && base64ToHex(r.target) === payload.base);
			const headSnap = refs.find(r => r.target && base64ToHex(r.target) === payload.head);

			const baseRef = baseSnap?.name || `snap.${payload.base.substring(0, 8)}`;
			const headRef = headSnap?.name || `snap.${payload.head.substring(0, 8)}`;

			// Load files from both snapshots
			const [baseData, headData] = await Promise.all([
				api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/files/${baseRef}`).catch(() => ({ files: [] })),
				api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/files/${headRef}`).catch(() => ({ files: [] }))
			]);

			const baseFiles = new Map((baseData.files || []).map(f => [f.path, f]));
			const headFiles = new Map((headData.files || []).map(f => [f.path, f]));

			const added = [];
			const removed = [];
			const modified = [];

			// Find added and modified files
			for (const [path, file] of headFiles) {
				if (!baseFiles.has(path)) {
					added.push(file);
				} else if (baseFiles.get(path).contentDigest !== file.contentDigest) {
					modified.push(file);
				}
			}

			// Find removed files
			for (const [path, file] of baseFiles) {
				if (!headFiles.has(path)) {
					removed.push(file);
				}
			}

			changesetFiles = { added, removed, modified };
		} catch (e) {
			console.error('Failed to load changeset diff:', e);
			changesetFiles = { added: [], removed: [], modified: [] };
		}

		changesetFilesLoading = false;
	}

	// Select a changeset for detail view
	async function selectChangeset(csRef) {
		selectedChangeset = csRef;
		await loadChangesetDiff(csRef);
	}

	// Close changeset detail view
	function closeChangesetDetail() {
		selectedChangeset = null;
		changesetFiles = { added: [], removed: [], modified: [] };
	}

	async function deleteRepo() {
		deleting = true;
		const result = await api('DELETE', `/api/v1/orgs/${$page.params.slug}/repos/${$page.params.repo}`);
		deleting = false;
		if (!result.error) {
			goto(`/orgs/${$page.params.slug}`);
		}
	}

	function getCloneUrl() {
		return `${window.location.origin}/${$page.params.slug}/${$page.params.repo}`;
	}

	function getQuickstart() {
		const cloneUrl = getCloneUrl();
		return `# Set up remote
kai remote set origin ${cloneUrl}

# Login (if not already)
kai auth login

# Push your latest snapshot
kai push origin snap.latest`;
	}

	function formatDate(timestamp) {
		if (!timestamp) return '-';
		// Timestamp is in milliseconds if > year 2100 in seconds
		const ms = timestamp > 4102444800 ? timestamp : timestamp * 1000;
		return new Date(ms).toLocaleString();
	}

	function shortHash(target) {
		if (!target) return '-';
		// target is base64 encoded, decode and show first 12 hex chars
		try {
			const bytes = atob(target);
			let hex = '';
			for (let i = 0; i < Math.min(bytes.length, 6); i++) {
				hex += bytes.charCodeAt(i).toString(16).padStart(2, '0');
			}
			return hex;
		} catch {
			return target.substring(0, 12);
		}
	}

	function getRefType(name) {
		if (name.startsWith('snap.')) return 'snapshot';
		if (name.startsWith('cs.')) return 'changeset';
		if (name.startsWith('ws.')) return 'workspace';
		return 'ref';
	}

	function getRefIcon(name) {
		if (name.startsWith('snap.')) return '📸';
		if (name.startsWith('cs.')) return '📝';
		if (name.startsWith('ws.')) return '🔀';
		return '🏷️';
	}

	// Filter refs by type
	let snapshots = $derived(refs.filter(r => r.name.startsWith('snap.')));
	let changesets = $derived(refs.filter(r => r.name.startsWith('cs.')));
	let workspaces = $derived(refs.filter(r => r.name.startsWith('ws.')));
	let otherRefs = $derived(refs.filter(r => !r.name.startsWith('snap.') && !r.name.startsWith('cs.') && !r.name.startsWith('ws.')));

	// Compare refs for diff
	async function compareDiff() {
		if (!compareBase || !compareHead) return;
		diffLoading = true;
		diffResult = null;

		// In the future, this could call a server-side diff API
		// For now, show a placeholder that explains CLI usage
		await new Promise(resolve => setTimeout(resolve, 500));

		diffResult = {
			base: compareBase,
			head: compareHead,
			message: 'Semantic diff is available via CLI',
			cliCommand: `kai diff @snap:${compareBase} @snap:${compareHead} --semantic`
		};
		diffLoading = false;
	}

	function getActionClass(action) {
		switch(action) {
			case 'added': return 'text-green-400';
			case 'removed': return 'text-red-400';
			case 'modified': return 'text-yellow-400';
			default: return '';
		}
	}

	function getActionIcon(action) {
		switch(action) {
			case 'added': return '+';
			case 'removed': return '-';
			case 'modified': return '~';
			default: return ' ';
		}
	}

	// Load a single file by path (fast)
	async function loadSingleFile(snapshotRef, filePath) {
		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/files/${snapshotRef}?path=${encodeURIComponent(filePath)}`);
		if (data.files && data.files.length > 0) {
			return data.files[0];
		}
		return null;
	}

	// Files tab functions
	async function loadFiles(snapshotRef, autoSelectReadme = true) {
		if (!snapshotRef) {
			files = [];
			return;
		}
		filesLoading = true;
		// Don't reset selectedFile/fileContent if already loaded
		if (!selectedFile) {
			fileContent = '';
		}
		expandedDirs = new Set(); // Reset expanded directories

		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/files/${snapshotRef}`);
		if (data.files) {
			files = data.files.sort((a, b) => a.path.localeCompare(b.path));

			// Auto-select README if no file is selected
			if (autoSelectReadme && !selectedFile && files.length > 0) {
				const readme = files.find(f => isReadme(f.path));
				if (readme) {
					setSelectedFile(readme);
				}
			}
		} else {
			files = [];
		}
		filesLoading = false;
	}

	async function loadFileContent(file) {
		selectedFile = file;
		fileContentLoading = true;
		fileContent = '';
		fileContentRaw = null;

		const data = await api('GET', `/${$page.params.slug}/${$page.params.repo}/v1/content/${file.digest}`);
		if (data.content) {
			// Store raw base64 for binary files (images)
			fileContentRaw = data.content;

			// Check file size (base64 is ~4/3 of original size)
			const estimatedSize = (data.content.length * 3) / 4;

			if (isImageFile(file.path) || isSvgFile(file.path)) {
				// For images and SVGs, keep as base64 for display
				if (isSvgFile(file.path)) {
					// Decode SVG to show the source as well
					try {
						fileContent = atob(data.content);
					} catch {
						fileContent = '';
					}
				}
			} else if (isBinaryFile(file.path)) {
				fileContent = '(Binary file - cannot display)';
			} else {
				// Text file - decode from base64
				try {
					fileContent = atob(data.content);
				} catch {
					fileContent = '(Binary file - cannot display)';
				}
			}
		}
		fileContentLoading = false;
	}

	// Build file tree structure
	function buildFileTree(fileList) {
		const tree = {};
		for (const file of fileList) {
			const parts = file.path.split('/');
			let current = tree;
			for (let i = 0; i < parts.length - 1; i++) {
				const part = parts[i];
				if (!current[part]) {
					current[part] = { _isDir: true, _children: {} };
				}
				current = current[part]._children;
			}
			const fileName = parts[parts.length - 1];
			current[fileName] = { _isDir: false, _file: file };
		}
		return tree;
	}

	// Get sorted entries from tree (directories first, then alphabetically)
	function getSortedEntries(tree) {
		const entries = Object.entries(tree);
		return entries.sort((a, b) => {
			const aIsDir = a[1]._isDir;
			const bIsDir = b[1]._isDir;
			if (aIsDir && !bIsDir) return -1;
			if (!aIsDir && bIsDir) return 1;
			return a[0].localeCompare(b[0]);
		});
	}

	// Toggle directory expansion
	function toggleDir(path) {
		const newExpanded = new Set(expandedDirs);
		if (newExpanded.has(path)) {
			newExpanded.delete(path);
		} else {
			newExpanded.add(path);
		}
		expandedDirs = newExpanded;
	}

	// Expand directories to show selected file
	function expandToFile(filePath) {
		if (!filePath) return;
		const parts = filePath.split('/');
		const newExpanded = new Set(expandedDirs);
		let path = '';
		for (let i = 0; i < parts.length - 1; i++) {
			path = path ? `${path}/${parts[i]}` : parts[i];
			newExpanded.add(path);
		}
		expandedDirs = newExpanded;
	}

	// Highlight code using highlight.js
	function highlightCode(code, lang) {
		if (!code) return '';
		try {
			// Try to highlight with the specified language
			if (lang && hljs.getLanguage(lang)) {
				return hljs.highlight(code, { language: lang }).value;
			}
			// Fallback to auto-detection
			return hljs.highlightAuto(code).value;
		} catch (e) {
			// If highlighting fails, return escaped HTML
			return code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
		}
	}

	// Generate line numbers HTML
	function getLineNumbers(code) {
		if (!code) return '';
		const lines = code.split('\n');
		return lines.map((_, i) => i + 1).join('\n');
	}

	let fileTree = $derived(buildFileTree(files));
	let highlightedContent = $derived(selectedFile ? highlightCode(fileContent, selectedFile.lang) : '');
	let lineNumbers = $derived(getLineNumbers(fileContent));
</script>

<div class="max-w-6xl mx-auto px-5 py-8">
	{#if loading || refsLoading}
		<div class="text-center py-12 text-kai-text-muted">Loading...</div>
	{:else if repo}
		<div class="mb-6">
			<a href="/orgs/{$page.params.slug}" class="text-kai-accent hover:underline">
				← Back to {$page.params.slug}
			</a>
		</div>

		<div class="flex justify-between items-start mb-6">
			<div>
				<h2 class="text-xl font-semibold">
					{$page.params.slug}/{$page.params.repo}
					<span class="badge badge-{repo.visibility} ml-2">{repo.visibility}</span>
				</h2>
			</div>
			<button
				class="btn btn-danger text-sm"
				onclick={() => showDeleteConfirm = true}
			>
				Delete
			</button>
		</div>

		<!-- Delete Confirmation Modal -->
		{#if showDeleteConfirm}
			<div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
				<div class="bg-kai-bg-secondary border border-kai-border rounded-lg p-6 max-w-md w-full mx-4">
					<h3 class="text-lg font-semibold mb-2">Delete Repository</h3>
					<p class="text-kai-text-muted mb-4">
						Are you sure you want to delete <strong>{$page.params.slug}/{$page.params.repo}</strong>?
						This action cannot be undone.
					</p>
					<div class="flex gap-3 justify-end">
						<button
							class="btn"
							onclick={() => showDeleteConfirm = false}
							disabled={deleting}
						>
							Cancel
						</button>
						<button
							class="btn btn-danger"
							onclick={deleteRepo}
							disabled={deleting}
						>
							{deleting ? 'Deleting...' : 'Delete'}
						</button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Empty state - GitHub/GitLab style setup instructions -->
		{#if refs.length === 0}
			<div class="border border-kai-border rounded-md">
				<!-- Quick setup header -->
				<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border">
					<h3 class="font-semibold">Quick setup</h3>
				</div>

				<div class="p-4 border-b border-kai-border">
					<div class="flex items-center gap-2 mb-2">
						<span class="text-kai-text-muted text-sm">Remote URL:</span>
					</div>
					<div class="flex gap-2 items-center">
						<input type="text" readonly value={getCloneUrl()} class="input flex-1 font-mono text-sm bg-kai-bg" />
						<button
							class="btn"
							onclick={() => {
								navigator.clipboard.writeText(getCloneUrl());
							}}
						>
							Copy
						</button>
					</div>
				</div>

				<!-- Push an existing repository -->
				<div class="p-4">
					<h4 class="font-medium mb-3">…or push from an existing Kai repository</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">kai remote set origin {getCloneUrl()}
kai auth login
kai push origin snap.latest</pre>
					</div>
				</div>

				<!-- Create new from command line -->
				<div class="p-4 border-t border-kai-border">
					<h4 class="font-medium mb-3">…or create a new snapshot from command line</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">cd your-project
kai init
kai snapshot main --repo .
kai analyze symbols @snap:last
kai remote set origin {getCloneUrl()}
kai auth login
kai push origin snap.latest</pre>
					</div>
				</div>
			</div>

		<!-- Has content - show tabs with snapshots/changesets -->
		{:else}
			<!-- Tabs -->
			<div class="border-b border-kai-border mb-6">
				<nav class="flex gap-4">
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'changes' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => setTab('changes')}
					>
						Changes
						{#if changesets.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{changesets.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'workspaces' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => setTab('workspaces')}
					>
						Workspaces
						{#if workspaces.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{workspaces.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'files' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => setTab('files')}
					>
						Files
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'snapshots' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => setTab('snapshots')}
					>
						Snapshots
						{#if snapshots.length > 0}
							<span class="ml-1 px-1.5 py-0.5 text-xs rounded-full bg-kai-bg-tertiary">{snapshots.length}</span>
						{/if}
					</button>
					<button
						class="px-3 py-2 text-sm font-medium border-b-2 -mb-px transition-colors {activeTab === 'setup' ? 'border-kai-accent text-kai-text' : 'border-transparent text-kai-text-muted hover:text-kai-text'}"
						onclick={() => setTab('setup')}
					>
						Setup
					</button>
				</nav>
			</div>

			<!-- Tab Content -->
			{#if activeTab === 'changes'}
				<!-- Changeset Detail View -->
				{#if selectedChangeset}
					{@const payload = changesetPayloads[selectedChangeset.name] || {}}
					<div class="border border-kai-border rounded-md">
						<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border flex items-center justify-between">
							<div class="flex items-center gap-3">
								<button
									class="text-kai-text-muted hover:text-kai-text"
									onclick={closeChangesetDetail}
								>
									<svg class="w-5 h-5" viewBox="0 0 20 20" fill="currentColor">
										<path fill-rule="evenodd" d="M12.79 5.23a.75.75 0 01-.02 1.06L8.832 10l3.938 3.71a.75.75 0 11-1.04 1.08l-4.5-4.25a.75.75 0 010-1.08l4.5-4.25a.75.75 0 011.06.02z" clip-rule="evenodd" />
									</svg>
								</button>
								<h3 class="font-semibold text-lg">{payload.intent || selectedChangeset.name}</h3>
							</div>
							<span class="text-sm text-kai-text-muted">{formatDate(selectedChangeset.updatedAt)}</span>
						</div>

						<div class="p-4">
							<!-- Intent and metadata -->
							<div class="mb-4 pb-4 border-b border-kai-border">
								<div class="flex items-center gap-4 text-sm text-kai-text-muted">
									<span class="font-mono">{selectedChangeset.name}</span>
									<span>by {selectedChangeset.actor || 'unknown'}</span>
								</div>
								{#if payload.description}
									<p class="mt-2 text-kai-text">{payload.description}</p>
								{/if}
							</div>

							<!-- File changes -->
							{#if changesetFilesLoading}
								<div class="text-center py-8 text-kai-text-muted">Loading changes...</div>
							{:else}
								{@const totalFiles = changesetFiles.added.length + changesetFiles.removed.length + changesetFiles.modified.length}
								{#if totalFiles === 0}
									<div class="text-center py-8 text-kai-text-muted">
										<p>No file changes detected</p>
										<p class="text-xs mt-1">This may be a workspace-only changeset</p>
									</div>
								{:else}
									<div class="space-y-3">
										<div class="text-sm text-kai-text-muted">
											{totalFiles} file{totalFiles !== 1 ? 's' : ''} changed
											{#if changesetFiles.added.length > 0}
												<span class="text-green-400 ml-2">+{changesetFiles.added.length} added</span>
											{/if}
											{#if changesetFiles.modified.length > 0}
												<span class="text-yellow-400 ml-2">~{changesetFiles.modified.length} modified</span>
											{/if}
											{#if changesetFiles.removed.length > 0}
												<span class="text-red-400 ml-2">-{changesetFiles.removed.length} removed</span>
											{/if}
										</div>

										<div class="space-y-1">
											{#each changesetFiles.added as file}
												<div class="flex items-center gap-2 text-sm py-1 px-2 rounded hover:bg-kai-bg-tertiary">
													<span class="text-green-400 font-mono w-4">+</span>
													<span class="text-kai-text font-mono">{file.path}</span>
												</div>
											{/each}
											{#each changesetFiles.modified as file}
												<div class="flex items-center gap-2 text-sm py-1 px-2 rounded hover:bg-kai-bg-tertiary">
													<span class="text-yellow-400 font-mono w-4">~</span>
													<span class="text-kai-text font-mono">{file.path}</span>
												</div>
											{/each}
											{#each changesetFiles.removed as file}
												<div class="flex items-center gap-2 text-sm py-1 px-2 rounded hover:bg-kai-bg-tertiary">
													<span class="text-red-400 font-mono w-4">-</span>
													<span class="text-kai-text font-mono">{file.path}</span>
												</div>
											{/each}
										</div>
									</div>
								{/if}
							{/if}
						</div>
					</div>

				<!-- Changeset List View -->
				{:else if changesets.length === 0}
					<div class="text-center py-12 text-kai-text-muted">
						<div class="mb-4">
							<svg class="w-12 h-12 mx-auto opacity-50" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
								<path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
							</svg>
						</div>
						<p class="text-lg mb-2">No changes yet</p>
						<p class="text-sm">Create a changeset with the CLI:</p>
						<code class="text-xs bg-kai-bg px-2 py-1 rounded mt-2 inline-block">kai changeset create snap.before snap.after</code>
					</div>
				{:else if changesetsLoading}
					<div class="text-center py-8 text-kai-text-muted">Loading changesets...</div>
				{:else}
					<div class="space-y-3">
						{#each changesets as ref}
							{@const payload = changesetPayloads[ref.name] || {}}
							<button
								class="w-full text-left border border-kai-border rounded-md p-4 hover:border-kai-accent transition-colors bg-kai-bg-secondary"
								onclick={() => selectChangeset(ref)}
							>
								<div class="flex items-start justify-between">
									<div class="flex-1">
										<h3 class="font-medium text-kai-text mb-1">
											{payload.intent || ref.name}
										</h3>
										<div class="flex items-center gap-3 text-sm text-kai-text-muted">
											<span class="font-mono text-xs">{ref.name}</span>
											<span>{ref.actor || 'unknown'}</span>
											<span>{formatDate(ref.updatedAt)}</span>
										</div>
									</div>
									<svg class="w-5 h-5 text-kai-text-muted" viewBox="0 0 20 20" fill="currentColor">
										<path fill-rule="evenodd" d="M7.21 14.77a.75.75 0 01.02-1.06L11.168 10 7.23 6.29a.75.75 0 111.04-1.08l4.5 4.25a.75.75 0 010 1.08l-4.5 4.25a.75.75 0 01-1.06-.02z" clip-rule="evenodd" />
									</svg>
								</div>
							</button>
						{/each}
					</div>
				{/if}

			{:else if activeTab === 'workspaces'}
				{#if workspaces.length === 0}
					<div class="text-center py-12 text-kai-text-muted">
						<div class="mb-4">
							<svg class="w-12 h-12 mx-auto opacity-50" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
								<path stroke-linecap="round" stroke-linejoin="round" d="M2.25 7.125C2.25 6.504 2.754 6 3.375 6h6c.621 0 1.125.504 1.125 1.125v3.75c0 .621-.504 1.125-1.125 1.125h-6a1.125 1.125 0 01-1.125-1.125v-3.75zM14.25 8.625c0-.621.504-1.125 1.125-1.125h5.25c.621 0 1.125.504 1.125 1.125v8.25c0 .621-.504 1.125-1.125 1.125h-5.25a1.125 1.125 0 01-1.125-1.125v-8.25zM3.75 16.125c0-.621.504-1.125 1.125-1.125h5.25c.621 0 1.125.504 1.125 1.125v2.25c0 .621-.504 1.125-1.125 1.125h-5.25a1.125 1.125 0 01-1.125-1.125v-2.25z" />
							</svg>
						</div>
						<p class="text-lg mb-2">No workspaces</p>
						<p class="text-sm">Create a workspace to start accumulating changes:</p>
						<code class="text-xs bg-kai-bg px-2 py-1 rounded mt-2 inline-block">kai ws create --name feature --base snap.main</code>
					</div>
				{:else}
					<div class="space-y-3">
						{#each workspaces as ref}
							<div class="border border-kai-border rounded-md p-4 bg-kai-bg-secondary">
								<div class="flex items-center justify-between">
									<div>
										<h3 class="font-medium text-kai-text">{ref.name}</h3>
										<div class="text-sm text-kai-text-muted mt-1">
											<span>{ref.actor || 'unknown'}</span>
											<span class="mx-2">-</span>
											<span>{formatDate(ref.updatedAt)}</span>
										</div>
									</div>
									<code class="text-xs bg-kai-bg px-1.5 py-0.5 rounded font-mono">{shortHash(ref.target)}</code>
								</div>
							</div>
						{/each}
					</div>
				{/if}

			{:else if activeTab === 'snapshots'}
				{#if snapshots.length === 0}
					<div class="text-center py-8 text-kai-text-muted">
						<p>No snapshots yet</p>
					</div>
				{:else}
					<div class="border border-kai-border rounded-md overflow-hidden">
						<table class="w-full">
							<thead class="bg-kai-bg-secondary">
								<tr class="text-left text-sm text-kai-text-muted">
									<th class="px-4 py-3 font-medium">Name</th>
									<th class="px-4 py-3 font-medium">Target</th>
									<th class="px-4 py-3 font-medium">Actor</th>
									<th class="px-4 py-3 font-medium">Updated</th>
								</tr>
							</thead>
							<tbody>
								{#each snapshots as ref}
									<tr class="border-t border-kai-border hover:bg-kai-bg-secondary">
										<td class="px-4 py-3">
											<span class="font-mono text-kai-accent">{ref.name}</span>
										</td>
										<td class="px-4 py-3">
											<code class="text-xs bg-kai-bg px-1.5 py-0.5 rounded font-mono">{shortHash(ref.target)}</code>
										</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{ref.actor || '-'}</td>
										<td class="px-4 py-3 text-kai-text-muted text-sm">{formatDate(ref.updatedAt)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			{:else if activeTab === 'files'}
				<div class="border border-kai-border rounded-md">
					<!-- Snapshot selector -->
					<div class="bg-kai-bg-secondary px-4 py-3 border-b border-kai-border">
						<div class="flex items-center gap-4">
							<label class="text-sm text-kai-text-muted">Snapshot:</label>
							<select
								bind:value={selectedSnapshot}
								onchange={(e) => setSnapshot(e.target.value)}
								class="input w-64 font-mono text-sm"
							>
								<option value="">Select a snapshot...</option>
								{#each snapshots as ref}
									<option value={ref.name}>{ref.name}</option>
								{/each}
							</select>
							{#if filesLoading}
								<span class="text-kai-text-muted text-sm">Loading...</span>
							{:else if files.length > 0}
								<span class="text-kai-text-muted text-sm">{files.length} files</span>
							{/if}
						</div>
					</div>

					{#if !selectedSnapshot}
						<div class="text-center py-12 text-kai-text-muted">
							<p>Select a snapshot to browse files</p>
						</div>
					{:else if filesLoading}
						<div class="text-center py-12 text-kai-text-muted">
							<p>Loading files...</p>
						</div>
					{:else if files.length === 0}
						<div class="text-center py-12 text-kai-text-muted">
							<p>No files in this snapshot</p>
							<p class="text-xs mt-2">This snapshot may have been created before file tracking was enabled.</p>
						</div>
					{:else}
						{#snippet renderTree(tree, parentPath = '')}
							{#each getSortedEntries(tree) as [name, node]}
								{@const fullPath = parentPath ? `${parentPath}/${name}` : name}
								{#if node._isDir}
									<!-- Directory -->
									<div>
										<button
											class="w-full text-left px-2 py-1 rounded text-sm hover:bg-kai-bg-tertiary transition-colors flex items-center gap-1.5 text-kai-text"
											onclick={() => toggleDir(fullPath)}
										>
											<svg class="w-3 h-3 text-kai-text-muted flex-shrink-0" viewBox="0 0 16 16" fill="currentColor">
												{#if expandedDirs.has(fullPath)}
													<path d="M4 6l4 4 4-4H4z"/>
												{:else}
													<path d="M6 4l4 4-4 4V4z"/>
												{/if}
											</svg>
											<svg class="w-4 h-4 flex-shrink-0" viewBox="0 0 16 16" fill="#519aba">
												<path d="M1.5 3A1.5 1.5 0 000 4.5v8A1.5 1.5 0 001.5 14h13a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5h-6l-1-1.5H1.5z"/>
											</svg>
											<span class="truncate">{name}</span>
										</button>
										{#if expandedDirs.has(fullPath)}
											<div class="ml-3">
												{@render renderTree(node._children, fullPath)}
											</div>
										{/if}
									</div>
								{:else}
									<!-- File -->
									<button
										class="w-full text-left px-2 py-1 rounded text-sm hover:bg-kai-bg-tertiary transition-colors flex items-center gap-1.5 {selectedFile?.digest === node._file.digest ? 'bg-kai-bg-tertiary text-kai-accent' : 'text-kai-text'}"
										onclick={() => setSelectedFile(node._file)}
									>
										<span class="w-3 flex-shrink-0"></span>
										<svg class="w-4 h-4 flex-shrink-0 text-kai-text-muted" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1">
											<path d="M3 2.5A1.5 1.5 0 014.5 1h5.086a1.5 1.5 0 011.06.44l2.915 2.914a1.5 1.5 0 01.439 1.06V13.5a1.5 1.5 0 01-1.5 1.5h-8A1.5 1.5 0 013 13.5v-11z"/>
											<path d="M9.5 1v3.5a1 1 0 001 1H14"/>
										</svg>
										<span class="truncate">{name}</span>
									</button>
								{/if}
							{/each}
						{/snippet}

						<div class="flex" style="min-height: 400px;">
							<!-- File tree -->
							<div class="w-72 border-r border-kai-border overflow-auto" style="max-height: 600px;">
								<div class="p-2">
									{@render renderTree(fileTree)}
								</div>
							</div>

							<!-- File content viewer -->
							<div class="flex-1 overflow-auto" style="max-height: 600px;">
								{#if !selectedFile}
									<div class="flex items-center justify-center h-full text-kai-text-muted">
										<p>Select a file to view</p>
									</div>
								{:else if fileContentLoading}
									<div class="flex items-center justify-center h-full text-kai-text-muted">
										<p>Loading...</p>
									</div>
								{:else}
									<div class="p-4">
										<div class="flex items-center justify-between mb-3 pb-2 border-b border-kai-border">
											<div class="flex items-center gap-2">
												<svg class="w-4 h-4 flex-shrink-0 text-kai-text-muted" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1">
													<path d="M3 2.5A1.5 1.5 0 014.5 1h5.086a1.5 1.5 0 011.06.44l2.915 2.914a1.5 1.5 0 01.439 1.06V13.5a1.5 1.5 0 01-1.5 1.5h-8A1.5 1.5 0 013 13.5v-11z"/>
													<path d="M9.5 1v3.5a1 1 0 001 1H14"/>
												</svg>
												<span class="text-sm">{selectedFile.path}</span>
												<span class="text-xs text-kai-text-muted px-2 py-0.5 bg-kai-bg rounded">{selectedFile.lang || getFileExtension(selectedFile.path)}</span>
											</div>
											<div class="flex gap-2">
												<button
													class="btn text-xs"
													onclick={() => navigator.clipboard.writeText(getCurrentFileLink())}
													title="Copy link to this file"
												>
													Copy Link
												</button>
												{#if !isBinaryFile(selectedFile.path) || isSvgFile(selectedFile.path)}
													<button
														class="btn text-xs"
														onclick={() => navigator.clipboard.writeText(fileContent)}
														title="Copy file contents"
													>
														Copy Code
													</button>
												{/if}
											</div>
										</div>

										<!-- Image preview -->
										{#if isImageFile(selectedFile.path) && fileContentRaw}
											<div class="flex flex-col items-center justify-center bg-kai-bg rounded border border-kai-border p-8">
												<img
													src="data:{getMimeType(selectedFile.path)};base64,{fileContentRaw}"
													alt={selectedFile.path}
													class="max-w-full max-h-96 object-contain rounded"
													style="background: repeating-conic-gradient(#333 0% 25%, #444 0% 50%) 50% / 16px 16px;"
												/>
												<p class="text-kai-text-muted text-sm mt-4">
													{selectedFile.path.split('/').pop()}
												</p>
											</div>
										<!-- SVG preview + source -->
										{:else if isSvgFile(selectedFile.path) && fileContentRaw}
											<div class="space-y-4">
												<!-- SVG rendered preview -->
												<div class="bg-kai-bg rounded border border-kai-border p-8">
													<p class="text-xs text-kai-text-muted mb-4">Preview</p>
													<div class="flex items-center justify-center" style="background: repeating-conic-gradient(#333 0% 25%, #444 0% 50%) 50% / 16px 16px; padding: 2rem; border-radius: 0.5rem;">
														<img
															src="data:image/svg+xml;base64,{fileContentRaw}"
															alt={selectedFile.path}
															class="max-w-full max-h-64 object-contain"
														/>
													</div>
												</div>
												<!-- SVG source code -->
												<div>
													<p class="text-xs text-kai-text-muted mb-2">Source</p>
													<div class="code-viewer bg-kai-bg rounded border border-kai-border overflow-auto" bind:this={codeViewerEl}>
														<table class="w-full code-table">
															<tbody>
																{#each fileContent.split('\n') as line, i}
																	{@const lineNum = i + 1}
																	{@const isSelected = isLineSelected(lineNum)}
																	<tr
																		class="code-line {isSelected ? 'line-selected' : ''}"
																		data-line={lineNum}
																	>
																		<td
																			class="line-number select-none text-right pr-3 pl-3 text-kai-text-muted border-r border-kai-border cursor-pointer hover:text-kai-accent"
																			onclick={(e) => handleLineClick(lineNum, e)}
																		>
																			<a href="#L{lineNum}" class="block" onclick={(e) => e.preventDefault()}>
																				{lineNum}
																			</a>
																		</td>
																		<td class="code-content pl-4 pr-4">
																			<pre class="text-sm font-mono whitespace-pre hljs">{@html highlightCode(line, 'xml') || ' '}</pre>
																		</td>
																	</tr>
																{/each}
															</tbody>
														</table>
													</div>
												</div>
											</div>
										<!-- Binary file message -->
										{:else if isBinaryFile(selectedFile.path)}
											<div class="flex flex-col items-center justify-center bg-kai-bg rounded border border-kai-border p-12 text-kai-text-muted">
												<svg xmlns="http://www.w3.org/2000/svg" class="h-12 w-12 mb-4 opacity-50" fill="none" viewBox="0 0 24 24" stroke="currentColor">
													<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 21h10a2 2 0 002-2V9.414a1 1 0 00-.293-.707l-5.414-5.414A1 1 0 0012.586 3H7a2 2 0 00-2 2v14a2 2 0 002 2z" />
												</svg>
												<p class="text-lg mb-2">Binary file</p>
												<p class="text-sm">This file type cannot be displayed in the browser</p>
											</div>
										<!-- Markdown rendering -->
										{:else if isMarkdownFile(selectedFile.path)}
											<div class="markdown-body bg-kai-bg rounded border border-kai-border p-6 overflow-auto">
												{@html marked(fileContent)}
											</div>
										<!-- Regular code view -->
										{:else}
											<div class="code-viewer bg-kai-bg rounded border border-kai-border overflow-auto" bind:this={codeViewerEl}>
												<table class="w-full code-table">
													<tbody>
														{#each fileContent.split('\n') as line, i}
															{@const lineNum = i + 1}
															{@const isSelected = isLineSelected(lineNum)}
															<tr
																class="code-line {isSelected ? 'line-selected' : ''}"
																data-line={lineNum}
															>
																<td
																	class="line-number select-none text-right pr-3 pl-3 text-kai-text-muted border-r border-kai-border cursor-pointer hover:text-kai-accent"
																	onclick={(e) => handleLineClick(lineNum, e)}
																>
																	<a href="#L{lineNum}" class="block" onclick={(e) => e.preventDefault()}>
																		{lineNum}
																	</a>
																</td>
																<td class="code-content pl-4 pr-4">
																	<pre class="text-sm font-mono whitespace-pre hljs">{@html highlightCode(line, selectedFile?.lang) || ' '}</pre>
																</td>
															</tr>
														{/each}
													</tbody>
												</table>
											</div>
										{/if}
									</div>
								{/if}
							</div>
						</div>
					{/if}
				</div>
			{:else if activeTab === 'setup'}
				<div class="border border-kai-border rounded-md p-4">
					<h4 class="font-medium mb-3">Clone URL</h4>
					<div class="flex gap-2 items-center mb-6">
						<input type="text" readonly value={getCloneUrl()} class="input flex-1 font-mono text-sm bg-kai-bg" />
						<button
							class="btn"
							onclick={() => {
								navigator.clipboard.writeText(getCloneUrl());
							}}
						>
							Copy
						</button>
					</div>

					<h4 class="font-medium mb-3">Push to this repository</h4>
					<div class="code-block bg-kai-bg">
						<pre class="text-sm">kai remote set origin {getCloneUrl()}
kai push origin snap.latest</pre>
					</div>
				</div>
			{/if}
		{/if}
	{/if}
</div>

<style>
	/* Syntax highlighting theme - dark mode matching Kai design */
	:global(.hljs) {
		color: #e4e4e7; /* zinc-200 */
		background: transparent;
	}

	/* Comments */
	:global(.hljs-comment),
	:global(.hljs-quote) {
		color: #71717a; /* zinc-500 */
		font-style: italic;
	}

	/* Keywords, tags */
	:global(.hljs-keyword),
	:global(.hljs-selector-tag),
	:global(.hljs-tag) {
		color: #f472b6; /* pink-400 */
	}

	/* Strings */
	:global(.hljs-string),
	:global(.hljs-template-variable),
	:global(.hljs-addition) {
		color: #4ade80; /* green-400 */
	}

	/* Numbers, built-in */
	:global(.hljs-number),
	:global(.hljs-literal),
	:global(.hljs-built_in) {
		color: #fb923c; /* orange-400 */
	}

	/* Functions, methods */
	:global(.hljs-title),
	:global(.hljs-title.function_),
	:global(.hljs-section) {
		color: #60a5fa; /* blue-400 */
	}

	/* Variables, attributes */
	:global(.hljs-variable),
	:global(.hljs-attr),
	:global(.hljs-attribute) {
		color: #c084fc; /* purple-400 */
	}

	/* Types, classes */
	:global(.hljs-type),
	:global(.hljs-class .hljs-title),
	:global(.hljs-title.class_) {
		color: #fbbf24; /* amber-400 */
	}

	/* Symbols, bullets */
	:global(.hljs-symbol),
	:global(.hljs-bullet),
	:global(.hljs-link) {
		color: #2dd4bf; /* teal-400 */
	}

	/* Meta, preprocessor */
	:global(.hljs-meta),
	:global(.hljs-selector-id),
	:global(.hljs-selector-class) {
		color: #818cf8; /* indigo-400 */
	}

	/* Deletion */
	:global(.hljs-deletion) {
		color: #f87171; /* red-400 */
	}

	/* Emphasis */
	:global(.hljs-emphasis) {
		font-style: italic;
	}

	:global(.hljs-strong) {
		font-weight: bold;
	}

	/* Code viewer specific styles */
	.code-viewer {
		max-height: 500px;
	}

	.code-table {
		border-collapse: collapse;
		border-spacing: 0;
	}

	.code-line {
		line-height: 1.5;
	}

	.code-line:hover {
		background: rgba(255, 255, 255, 0.03);
	}

	.code-line.line-selected {
		background: rgba(250, 204, 21, 0.15); /* yellow highlight */
	}

	.code-line.line-selected .line-number {
		color: #fbbf24; /* amber-400 */
		font-weight: 600;
	}

	.line-number {
		background: rgba(0, 0, 0, 0.2);
		min-width: 3rem;
		font-size: 0.75rem;
		font-family: ui-monospace, monospace;
		vertical-align: top;
		padding-top: 0.125rem;
		padding-bottom: 0.125rem;
	}

	.line-number a {
		text-decoration: none;
		color: inherit;
	}

	.code-content {
		vertical-align: top;
		padding-top: 0.125rem;
		padding-bottom: 0.125rem;
	}

	.code-content pre {
		margin: 0;
		padding: 0;
	}

	/* GitHub-style markdown rendering for dark mode */
	.markdown-body {
		color: #e4e4e7;
		line-height: 1.6;
		max-height: 500px;
	}

	.markdown-body :global(h1),
	.markdown-body :global(h2),
	.markdown-body :global(h3),
	.markdown-body :global(h4),
	.markdown-body :global(h5),
	.markdown-body :global(h6) {
		margin-top: 1.5em;
		margin-bottom: 0.5em;
		font-weight: 600;
		line-height: 1.25;
		color: #fafafa;
	}

	.markdown-body :global(h1) {
		font-size: 2em;
		padding-bottom: 0.3em;
		border-bottom: 1px solid #3f3f46;
	}

	.markdown-body :global(h2) {
		font-size: 1.5em;
		padding-bottom: 0.3em;
		border-bottom: 1px solid #3f3f46;
	}

	.markdown-body :global(h3) { font-size: 1.25em; }
	.markdown-body :global(h4) { font-size: 1em; }
	.markdown-body :global(h5) { font-size: 0.875em; }
	.markdown-body :global(h6) { font-size: 0.85em; color: #a1a1aa; }

	.markdown-body :global(p) {
		margin-top: 0;
		margin-bottom: 1em;
	}

	.markdown-body :global(a) {
		color: #60a5fa;
		text-decoration: none;
	}

	.markdown-body :global(a:hover) {
		text-decoration: underline;
	}

	.markdown-body :global(code) {
		background: #27272a;
		padding: 0.2em 0.4em;
		border-radius: 4px;
		font-size: 0.875em;
		font-family: ui-monospace, monospace;
	}

	.markdown-body :global(pre) {
		background: #18181b;
		padding: 1em;
		border-radius: 6px;
		overflow-x: auto;
		margin: 1em 0;
	}

	.markdown-body :global(pre code) {
		background: transparent;
		padding: 0;
		font-size: 0.875em;
	}

	.markdown-body :global(blockquote) {
		margin: 1em 0;
		padding: 0 1em;
		color: #a1a1aa;
		border-left: 4px solid #3f3f46;
	}

	.markdown-body :global(ul),
	.markdown-body :global(ol) {
		margin: 1em 0;
		padding-left: 2em;
	}

	.markdown-body :global(li) {
		margin: 0.25em 0;
	}

	.markdown-body :global(li + li) {
		margin-top: 0.25em;
	}

	.markdown-body :global(hr) {
		border: none;
		border-top: 1px solid #3f3f46;
		margin: 1.5em 0;
	}

	.markdown-body :global(table) {
		border-collapse: collapse;
		width: 100%;
		margin: 1em 0;
	}

	.markdown-body :global(th),
	.markdown-body :global(td) {
		border: 1px solid #3f3f46;
		padding: 0.5em 1em;
		text-align: left;
	}

	.markdown-body :global(th) {
		background: #27272a;
		font-weight: 600;
	}

	.markdown-body :global(img) {
		max-width: 100%;
		height: auto;
		border-radius: 6px;
	}

	.markdown-body :global(strong) {
		font-weight: 600;
		color: #fafafa;
	}

	.markdown-body :global(em) {
		font-style: italic;
	}

	.markdown-body > :global(*:first-child) {
		margin-top: 0;
	}

	.markdown-body > :global(*:last-child) {
		margin-bottom: 0;
	}
</style>
