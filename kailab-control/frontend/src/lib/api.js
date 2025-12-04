import { get } from 'svelte/store';
import { accessToken, refreshToken, currentUser } from './stores.js';
import { goto } from '$app/navigation';

const API_BASE = '';

export async function api(method, path, body = null) {
	const token = get(accessToken);
	const headers = {
		'Content-Type': 'application/json'
	};

	if (token) {
		headers['Authorization'] = `Bearer ${token}`;
	}

	const options = { method, headers };
	if (body) {
		options.body = JSON.stringify(body);
	}

	const response = await fetch(API_BASE + path, options);

	if (response.status === 401 && get(refreshToken)) {
		const refreshed = await refreshAccessToken();
		if (refreshed) {
			headers['Authorization'] = `Bearer ${get(accessToken)}`;
			const retryResponse = await fetch(API_BASE + path, {
				method,
				headers,
				body: body ? JSON.stringify(body) : null
			});
			return retryResponse.json();
		}
	}

	if (response.status === 204) {
		return {};
	}

	return response.json();
}

async function refreshAccessToken() {
	const refresh = get(refreshToken);
	if (!refresh) return false;

	try {
		const response = await fetch(API_BASE + '/api/v1/auth/refresh', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ refresh_token: refresh })
		});

		if (response.ok) {
			const data = await response.json();
			accessToken.set(data.access_token);
			return true;
		}
	} catch (e) {
		console.error('Failed to refresh token', e);
	}

	logout();
	return false;
}

export function logout() {
	accessToken.set(null);
	refreshToken.set(null);
	currentUser.set(null);
	goto('/login');
}

export async function loadUser() {
	const data = await api('GET', '/api/v1/me');

	if (data.error) {
		logout();
		return null;
	}

	currentUser.set(data);
	return data;
}
