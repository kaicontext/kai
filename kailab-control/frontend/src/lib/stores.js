import { writable } from 'svelte/store';
import { browser } from '$app/environment';

// Auth stores
export const accessToken = writable(browser ? localStorage.getItem('accessToken') : null);
export const refreshToken = writable(browser ? localStorage.getItem('refreshToken') : null);
export const currentUser = writable(null);

// Persist tokens to localStorage
if (browser) {
	accessToken.subscribe(value => {
		if (value) {
			localStorage.setItem('accessToken', value);
		} else {
			localStorage.removeItem('accessToken');
		}
	});

	refreshToken.subscribe(value => {
		if (value) {
			localStorage.setItem('refreshToken', value);
		} else {
			localStorage.removeItem('refreshToken');
		}
	});
}

// Navigation state
export const currentOrg = writable(null);
export const currentRepo = writable(null);
