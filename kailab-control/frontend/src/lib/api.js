import { get } from 'svelte/store';
import { currentUser } from './stores.js';
import { goto } from '$app/navigation';

const API_BASE = '';

export async function api(method, path, body = null) {
	const headers = {
		'Content-Type': 'application/json'
	};

	const options = {
		method,
		headers,
		credentials: 'include' // Send cookies with requests
	};

	if (body) {
		options.body = JSON.stringify(body);
	}

	const response = await fetch(API_BASE + path, options);

	if (response.status === 401) {
		// Try to refresh the token via cookie
		const refreshed = await refreshAccessToken();
		if (refreshed) {
			const retryResponse = await fetch(API_BASE + path, {
				method,
				headers,
				credentials: 'include',
				body: body ? JSON.stringify(body) : null
			});
			if (retryResponse.status === 204) {
				return {};
			}
			return retryResponse.json();
		}
	}

	if (response.status === 204) {
		return {};
	}

	return response.json();
}

async function refreshAccessToken() {
	try {
		// Refresh token is sent via cookie automatically
		const response = await fetch(API_BASE + '/api/v1/auth/refresh', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			credentials: 'include',
			body: JSON.stringify({}) // Empty body, refresh token is in cookie
		});

		if (response.ok) {
			return true; // New access token is set via cookie
		}
	} catch (e) {
		console.error('Failed to refresh token', e);
	}

	return false;
}

export async function logout() {
	try {
		await fetch(API_BASE + '/api/v1/auth/logout', {
			method: 'POST',
			credentials: 'include'
		});
	} catch (e) {
		// Ignore errors
	}
	currentUser.set(null);
	goto('/login');
}

export async function loadUser() {
	const data = await api('GET', '/api/v1/me');

	if (data.error) {
		return null;
	}

	currentUser.set(data);
	return data;
}

// Check if user is authenticated (based on cookie presence)
export async function checkAuth() {
	const data = await api('GET', '/api/v1/me');
	if (data.error) {
		return false;
	}
	currentUser.set(data);
	return true;
}
