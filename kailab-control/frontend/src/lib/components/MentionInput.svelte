<script>
	import { api } from '$lib/api.js';

	let {
		value = $bindable(''),
		placeholder = '',
		rows = 3,
		org = '',
		class: className = ''
	} = $props();

	let textareaEl = $state(null);
	let showSuggestions = $state(false);
	let suggestions = $state([]);
	let selectedIndex = $state(0);
	let mentionStart = $state(-1);
	let mentionQuery = $state('');
	let loading = $state(false);
	let dropdownStyle = $state('');

	async function searchMembers(query) {
		if (!org || query.length < 1) {
			suggestions = [];
			return;
		}
		loading = true;
		try {
			const data = await api('GET', `/api/v1/orgs/${org}/members/search?q=${encodeURIComponent(query)}`);
			if (data?.users) {
				suggestions = data.users;
			}
		} catch (e) {
			console.error('Failed to search members', e);
			suggestions = [];
		}
		loading = false;
	}

	function handleInput(e) {
		const textarea = e.target;
		const cursorPos = textarea.selectionStart;
		const textBefore = value.substring(0, cursorPos);

		// Check if we're in a mention context
		const lastAt = textBefore.lastIndexOf('@');
		if (lastAt !== -1) {
			// Check if there's a space or newline between @ and cursor
			const textAfterAt = textBefore.substring(lastAt + 1);
			if (!/[\s]/.test(textAfterAt)) {
				// We're in a mention
				mentionStart = lastAt;
				mentionQuery = textAfterAt;
				showSuggestions = true;
				selectedIndex = 0;
				searchMembers(mentionQuery);
				updateDropdownPosition(textarea);
				return;
			}
		}

		// Not in a mention context
		showSuggestions = false;
		mentionStart = -1;
		mentionQuery = '';
	}

	function updateDropdownPosition(textarea) {
		// Position dropdown below the textarea
		const rect = textarea.getBoundingClientRect();
		dropdownStyle = `top: ${textarea.offsetHeight + 4}px; left: 0; width: 100%;`;
	}

	function handleKeydown(e) {
		if (!showSuggestions || suggestions.length === 0) return;

		if (e.key === 'ArrowDown') {
			e.preventDefault();
			selectedIndex = (selectedIndex + 1) % suggestions.length;
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			selectedIndex = (selectedIndex - 1 + suggestions.length) % suggestions.length;
		} else if (e.key === 'Enter' || e.key === 'Tab') {
			if (showSuggestions && suggestions.length > 0) {
				e.preventDefault();
				selectSuggestion(suggestions[selectedIndex]);
			}
		} else if (e.key === 'Escape') {
			showSuggestions = false;
		}
	}

	function selectSuggestion(user) {
		if (mentionStart === -1) return;

		const before = value.substring(0, mentionStart);
		const after = value.substring(mentionStart + 1 + mentionQuery.length);

		// Use email prefix as the mention
		const emailPrefix = user.email.split('@')[0];
		value = before + '@' + emailPrefix + ' ' + after;

		showSuggestions = false;
		mentionStart = -1;
		mentionQuery = '';

		// Focus back on textarea
		if (textareaEl) {
			textareaEl.focus();
			const newCursorPos = before.length + emailPrefix.length + 2; // +2 for @ and space
			textareaEl.setSelectionRange(newCursorPos, newCursorPos);
		}
	}

	function handleBlur() {
		// Delay hiding to allow click on suggestion
		setTimeout(() => {
			showSuggestions = false;
		}, 200);
	}
</script>

<div class="relative">
	<textarea
		bind:this={textareaEl}
		bind:value
		{placeholder}
		{rows}
		class="{className}"
		oninput={handleInput}
		onkeydown={handleKeydown}
		onblur={handleBlur}
	></textarea>

	{#if showSuggestions && (suggestions.length > 0 || loading)}
		<div
			class="absolute z-50 bg-kai-bg-secondary border border-kai-border rounded-lg shadow-lg overflow-hidden"
			style={dropdownStyle}
		>
			{#if loading && suggestions.length === 0}
				<div class="px-3 py-2 text-sm text-kai-text-muted">Searching...</div>
			{:else}
				{#each suggestions as user, i}
					<button
						type="button"
						class="w-full px-3 py-2 text-left text-sm hover:bg-kai-bg-tertiary flex items-center gap-2 {i === selectedIndex ? 'bg-kai-bg-tertiary' : ''}"
						onmousedown={() => selectSuggestion(user)}
					>
						<img
							src={user.avatar_url}
							alt=""
							class="w-6 h-6 rounded-full bg-kai-bg-tertiary"
						/>
						<div class="flex-1 min-w-0">
							{#if user.name}
								<div class="font-medium truncate">{user.name}</div>
								<div class="text-xs text-kai-text-muted truncate">{user.email}</div>
							{:else}
								<div class="font-medium truncate">{user.email}</div>
							{/if}
						</div>
					</button>
				{/each}
			{/if}
		</div>
	{/if}
</div>
